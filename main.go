package main

import (
	"bytes"
	"errors"
	"flag"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v2"
)

const (
	// Generate204URL generate HTTP 204 response
	Generate204URL = "https://www.google.com/generate_204"
)

// Config is the main config
type Config struct {
	Interval  string `yaml:"interval"`
	DownTimes int    `yaml:"down_times"`
	interval  time.Duration
	Server    struct {
		Hostname     string `yaml:"hostname"`
		Username     string `yaml:"username"`
		Password     string `yaml:"password"`
		ResetCommand string `yaml:"reset_command"`
	} `yaml:"server"`
}

// ErrorStatusCodeIsNot204 is a status code error
var ErrorStatusCodeIsNot204 error
var confFilePath string
var conf Config

func init() {
	ErrorStatusCodeIsNot204 = errors.New("response status code is not 204")
	flag.StringVar(&confFilePath, "config", "", "config file path")
}

func resetServer() (string, error) {
	// Authentication
	config := &ssh.ClientConfig{
		User: conf.Server.Username,
		Auth: []ssh.AuthMethod{
			ssh.Password(conf.Server.Password),
		},
		// alternatively, we could use a public key
		/*
			Auth: []ssh.AuthMethod{
				ssh.PublicKeys(key),
			},
		*/
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

func request204() error {
	resp, err := http.Get(Generate204URL)
	if err != nil {
		return err
	}
	if resp.Body != nil {
		// for reusing connection
		ioutil.ReadAll(resp.Body)
		resp.Body.Close()
	}
	if resp.StatusCode != http.StatusNoContent {
		return ErrorStatusCodeIsNot204
	}
	return err
}

func loopCheck() {
	ticker := time.NewTicker(conf.interval)
	defer ticker.Stop()

	var err error
	counter := 0
	for range ticker.C {
		err = request204()
		if err != nil {
			counter++
		} else {
			// mark health
			counter = 0
		}
		if counter >= conf.DownTimes {
			log.Println("resetting server...")

			var s string
			s, err = resetServer()
			log.Println(s)

			if err == nil {
				// reset counter
				counter = 0
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
		panic(err)
	}

	data, err := ioutil.ReadAll(confFile)
	if err != nil {
		panic(err)
	}

	err = yaml.Unmarshal(data, &conf)
	if err != nil {
		panic(err)
	}

	conf.interval, err = time.ParseDuration(conf.Interval)
	if err != nil {
		panic(err)
	}

	if conf.DownTimes <= 0 {
		err = errors.New("invalid down times")
		panic(err)
	}

	loopCheck()
}
