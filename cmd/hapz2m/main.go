package main

import (
	"hapz2m"

	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"syscall"
)

var (
	// matches whole line comments in config file
	CONFIG_COMMENTS_RE = regexp.MustCompile(`(?m)^\s*//.*$`)

	// for MQTT server URI validation
	SERVER_URL_RE = regexp.MustCompile(`^[a-z]+://.*:[0-9]{1,5}$`)
)

var (
	configFile = flag.String("config", "/etc/hapz2m.conf", "config file")
	dbPath     = flag.String("db", "/var/lib/hapz2m", "db path")
)

type config struct {
	Server, Username, Password string
}

func parseConfig(fname string) (cfg *config, err error) {
	cfgStr, err := os.ReadFile(fname)
	if err != nil {
		return
	}

	// remove line comments, json.Unmarshal can't parse them
	cfgStr = CONFIG_COMMENTS_RE.ReplaceAllLiteral(cfgStr, []byte{})

	cfg = &config{}
	if err = json.Unmarshal(cfgStr, cfg); err != nil {
		return
	}

	// sanity check
	if cfg.Server == "" {
		err = fmt.Errorf("MQTT server not specified")
	} else if !SERVER_URL_RE.MatchString(cfg.Server) {
		err = fmt.Errorf("invalid MQTT server: needs to be in URL format with port")
	}

	return
}

func main() {
	flag.Parse()

	cfg, err := parseConfig(*configFile)
	if err != nil {
		log.Fatalf("config file error: %v", err)
	}

	ctx, shutdown := context.WithCancel(context.Background())

	br := hapz2m.NewBridge(ctx, *dbPath)
	br.Server = cfg.Server
	br.Username = cfg.Username
	br.Password = cfg.Password

	err = br.ConnectMQTT()
	if err != nil {
		log.Printf("cannot connect to MQTT: %s", err)
		return
	}

	// listen for termination signals
	c := make(chan os.Signal, 1) // use `1` here to appear go vet: https://github.com/golang/go/issues/45604
	signal.Notify(c, os.Interrupt)
	signal.Notify(c, syscall.SIGTERM)
	go func() {
		<-c
		signal.Stop(c)
		shutdown()
	}()

	br.WaitConfigured()

	log.Println("hapz2m configured. starting HAP server...")

	err = br.StartHAP()
	if err != nil {
		if err == http.ErrServerClosed {
			log.Printf("HAP server was shutdown")
		} else {
			log.Printf("error starting server: %v", err)
		}
	}
}
