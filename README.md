# network-watchdog

A watchdog to check network status periodically, and run command on remote server when network is down.

## Usage

```sh
go get github.com/tengattack/network-watchdog
cp ${GOPATH:-$HOME/go}/src/github.com/tengattack/network-watchdog/config.example.yml config.yml
# edit the config.yml ...
# then run
network-watchdog -config=config.yml
```
