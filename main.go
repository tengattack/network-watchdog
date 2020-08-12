package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	ping "github.com/sparrc/go-ping"
	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v2"
)

const (
	// DefaultGenerate204ProbeURL is the default generate HTTP 204 response URL for probe
	DefaultGenerate204ProbeURL = "https://www.google.com/generate_204"
)

// ProbeConfig is the probe config
type ProbeConfig struct {
	Name      string `yaml:"name"`
	ProbeURL  string `yaml:"probe_url"`
	Timeout   string `yaml:"timeout"`
	Interval  string `yaml:"interval"`
	DownTimes int    `yaml:"down_times"`

	Server struct {
		Hostname     string `yaml:"hostname"`
		Username     string `yaml:"username"`
		Password     string `yaml:"password"`
		KeyFile      string `yaml:"key_file"`
		ResetCommand string `yaml:"reset_command"`
	} `yaml:"server"`

	timeout    time.Duration
	interval   time.Duration
	httpClient *http.Client
}

// Config is the main config
type Config struct {
	Probes []ProbeConfig `yaml:"probes"`
}

// errors
var (
	ErrorStatusCodeIsNot204  = errors.New("response status code is not 204")
	ErrorPingProbeUnfinished = errors.New("ping probe unfinished")
)

var confFilePath string
var verbose bool

func init() {
	flag.StringVar(&confFilePath, "config", "", "config file path")
	flag.BoolVar(&verbose, "verbose", false, "verbose mode")
}

// PublicKeyFile get ssh key from file
func PublicKeyFile(file string) (ssh.AuthMethod, error) {
	buffer, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, err
	}

	key, err := ssh.ParsePrivateKey(buffer)
	if err != nil {
		return nil, err
	}
	return ssh.PublicKeys(key), nil
}

func resetServer(conf *ProbeConfig) (string, error) {
	// Authentication
	var method []ssh.AuthMethod
	if conf.Server.Password != "" {
		method = append(method, ssh.Password(conf.Server.Password))
	}
	if conf.Server.KeyFile != "" {
		// alternatively, we could use a public key
		authMethod, err := PublicKeyFile(conf.Server.KeyFile)
		if err != nil {
			return "", err
		}
		method = append(method, authMethod)
	}
	config := &ssh.ClientConfig{
		User: conf.Server.Username,
		Auth: method,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			return nil
		},
	}
	var addr string
	if strings.LastIndex(conf.Server.Hostname, ":") >= 0 {
		addr = conf.Server.Hostname
	} else {
		// using ssh default port 22
		addr = conf.Server.Hostname + ":22"
	}
	// Connect
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return "", err
	}
	// Create a session. It is one session per command.
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	var b bytes.Buffer  // import "bytes"
	session.Stdout = &b // get output
	// you can also pass what gets input to the stdin, allowing you to pipe
	// content from client to server
	//      session.Stdin = bytes.NewBufferString("My input")

	// Finally, run the command
	err = session.Run(conf.Server.ResetCommand)
	return b.String(), err
}

func pingProbe(conf *ProbeConfig) error {
	parts := strings.SplitN(conf.ProbeURL, " ", 2)
	if len(parts) < 2 {
		panic("malformed ping probe url")
	}

	target := parts[1]
	pinger, err := ping.NewPinger(target)
	if err != nil {
		return err
	}

	pinger.SetPrivileged(true)
	pinger.Count = 3
	pinger.Timeout = 5 * time.Second

	if verbose {
		pinger.OnRecv = func(pkt *ping.Packet) {
			fmt.Printf("%d bytes from %s: icmp_seq=%d time=%v\n",
				pkt.Nbytes, pkt.IPAddr, pkt.Seq, pkt.Rtt)
		}
		pinger.OnFinish = func(stats *ping.Statistics) {
			fmt.Printf("\n--- %s ping statistics ---\n", stats.Addr)
			fmt.Printf("%d packets transmitted, %d packets received, %v%% packet loss\n",
				stats.PacketsSent, stats.PacketsRecv, stats.PacketLoss)
			fmt.Printf("round-trip min/avg/max/stddev = %v/%v/%v/%v\n",
				stats.MinRtt, stats.AvgRtt, stats.MaxRtt, stats.StdDevRtt)
		}
	}

	pinger.Run()
	if pinger.PacketsRecv < pinger.Count {
		return ErrorPingProbeUnfinished
	}

	return nil
}

func requestProbe(conf *ProbeConfig) error {
	req, err := http.NewRequest(http.MethodGet, conf.ProbeURL, nil)
	if err != nil {
		return err
	}
	if conf.httpClient == nil {
		conf.httpClient = &http.Client{Timeout: conf.timeout}
	}
	resp, err := conf.httpClient.Do(req)
	if err != nil {
		return err
	}
	if resp.Body != nil {
		// for reusing connection
		ioutil.ReadAll(resp.Body)
		resp.Body.Close()
	}
	if resp.StatusCode == http.StatusOK {
		// Allow 200 response code
		return nil
	}
	if resp.StatusCode != http.StatusNoContent {
		return ErrorStatusCodeIsNot204
	}
	return err
}

func loopCheck(stopCh <-chan struct{}, conf *ProbeConfig) {
	ticker := time.NewTicker(conf.interval)
	defer ticker.Stop()

	log.Println("starting server", conf.Name, "probe check")

	var err error
	counter := 0
loop:
	for {
		select {
		case <-stopCh:
			break loop
		case <-ticker.C:
			if strings.HasPrefix(conf.ProbeURL, "ping ") {
				err = pingProbe(conf)
			} else {
				err = requestProbe(conf)
			}
			if err != nil {
				counter++
				log.Println("server", conf.Name, "probe check error:", err, "counter:", counter)
			} else {
				// mark health
				counter = 0
				if verbose {
					log.Println("server", conf.Name, "probe check success")
				}
			}
			if counter >= conf.DownTimes {
				log.Println("resetting server", conf.Name, "...")

				var s string
				s, err = resetServer(conf)
				log.Println(s)

				if err == nil {
					// reset counter
					counter = 0
				} else {
					log.Println("resetting server", conf.Name, "error:", err)
				}
			}
		}
	}
}

func main() {
	flag.Parse()

	if confFilePath == "" {
		flag.Usage()
		os.Exit(1)
		return
	}

	confFile, err := os.Open(confFilePath)
	if err != nil {
		log.Fatal(err)
	}

	data, err := ioutil.ReadAll(confFile)
	if err != nil {
		log.Fatal(err)
	}

	var conf Config
	err = yaml.Unmarshal(data, &conf)
	if err != nil {
		log.Fatal(err)
	}

	if len(conf.Probes) <= 0 {
		err = errors.New("no probes configured")
		log.Fatal(err)
	}

	for i := range conf.Probes {
		if conf.Probes[i].Name == "" {
			conf.Probes[i].Name = conf.Probes[i].Server.Hostname
		}
		if conf.Probes[i].ProbeURL == "" {
			conf.Probes[i].ProbeURL = DefaultGenerate204ProbeURL
		}
		conf.Probes[i].timeout, err = time.ParseDuration(conf.Probes[i].Timeout)
		if err != nil {
			log.Fatal(err)
		}
		conf.Probes[i].interval, err = time.ParseDuration(conf.Probes[i].Interval)
		if err != nil {
			log.Fatal(err)
		}
		if conf.Probes[i].DownTimes <= 0 {
			err = errors.New("invalid down times")
			log.Fatal(err)
		}
	}

	stopCh := make(chan struct{})
	for i := range conf.Probes {
		go loopCheck(stopCh, &conf.Probes[i])
	}

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	<-shutdown
	close(stopCh)
}
