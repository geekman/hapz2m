package main

import (
	"hapz2m"

	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
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
	dbPath     = flag.String("db", "/var/lib/hapz2m/db", "db path")
	debugMode  = flag.Bool("debug", false, "enable debug messages")
	quietMode  = flag.Bool("quiet", false, "reduce verbosity by not showing received upates")
)

// config struct
type config struct {
	ListenAddr string
	Interfaces []string

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

	// check if we are running under systemd, and if so, dont output timestamps
	if a, b := os.Getenv("INVOCATION_ID"), os.Getenv("JOURNAL_STREAM"); a != "" && b != "" {
		log.SetFlags(0)
	}

	cfg, err := parseConfig(*configFile)
	if err != nil {
		log.Fatalf("config file error: %v", err)
	}

	ctx, shutdown := context.WithCancel(context.Background())

	br := hapz2m.NewBridge(ctx, *dbPath)
	br.Server = cfg.Server
	br.Username = cfg.Username
	br.Password = cfg.Password
	br.DebugMode = *debugMode
	br.QuietMode = *quietMode

	if *debugMode && *quietMode {
		log.Fatalf("-quiet and -debug options are mutually-exclusive")
	}

	// validate ListenAddr if specified
	if cfg.ListenAddr != "" {
		_, _, err := net.SplitHostPort(cfg.ListenAddr)
		if err != nil {
			log.Fatalf("invalid ListenAddr: %v", err)
		}
		br.ListenAddr = cfg.ListenAddr
	}

	br.Interfaces = cfg.Interfaces

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
