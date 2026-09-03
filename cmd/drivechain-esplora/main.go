// Command drivechain-esplora indexes one rust sidechain into Postgres and
// serves the Esplora REST API over it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/octobocto/drivechain-esplora/internal/api"
	"github.com/octobocto/drivechain-esplora/internal/chain"
	_ "github.com/octobocto/drivechain-esplora/internal/chain/register"
	"github.com/octobocto/drivechain-esplora/internal/index"
	"github.com/octobocto/drivechain-esplora/internal/mainchain"
	"github.com/octobocto/drivechain-esplora/internal/rpc"
	"github.com/octobocto/drivechain-esplora/internal/service"
	"github.com/octobocto/drivechain-esplora/internal/store"
)

// shutdownGrace is how long an in-flight request has to finish at stop.
const shutdownGrace = 10 * time.Second

type config struct {
	chainName   string
	network     string
	nodeURL     string
	enforcerURL string
	databaseURL string
	listen      string
	logLevel    string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := readConfig()
	if err != nil {
		return err
	}

	log, err := newLogger(cfg.logLevel)
	if err != nil {
		return err
	}

	network, err := chain.ParseNetwork(cfg.network)
	if err != nil {
		return err
	}
	spec, decoder, err := chain.Lookup(cfg.chainName)
	if err != nil {
		return err
	}

	nodeURL := cfg.nodeURL
	if nodeURL == "" {
		port, err := spec.NodeRPCPort(network)
		if err != nil {
			return err
		}
		nodeURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	}
	listen := cfg.listen
	if listen == "" {
		port, err := spec.APIPort(network)
		if err != nil {
			return err
		}
		listen = fmt.Sprintf("127.0.0.1:%d", port)
	}

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Neither the node nor the database has to be up at start. Each one sits
	// behind a wrapper that keeps trying.
	connect := func(ctx context.Context) (*chain.Node, error) {
		node := chain.NewNode(rpc.New(nodeURL))
		if _, err := node.TipHeight(ctx); err != nil &&
			!errors.Is(err, chain.ErrEmptyChain) {
			return nil, err
		}
		return node, nil
	}
	nodes := service.New("node", log, connect, nil)
	// The mempool poller holds its own connection, so a slow block read never
	// delays an unconfirmed payment.
	mempoolNodes := service.New("mempool node", log, connect, nil)

	stores := service.New("database", log,
		func(ctx context.Context) (*store.Store, error) {
			st, err := store.Open(ctx, cfg.databaseURL)
			if err != nil {
				return nil, err
			}
			if err := st.Init(ctx, cfg.chainName, network); err != nil {
				st.Close()
				return nil, err
			}
			return st, nil
		},
		func(st *store.Store) { st.Close() })

	go nodes.Run(ctx)
	go mempoolNodes.Run(ctx)
	go stores.Run(ctx)

	syncer := index.NewSyncer(nodes, stores, decoder, nil, log)
	go func() {
		if err := syncer.Run(ctx); err != nil && ctx.Err() == nil {
			log.Error("sync stopped", "error", err)
		}
	}()

	mempool := index.NewMempoolSyncer(mempoolNodes, stores, decoder, log)
	go func() {
		if err := mempool.Run(ctx); err != nil && ctx.Err() == nil {
			log.Error("mempool sync stopped", "error", err)
		}
	}()

	// The enforcer holds the mainchain's view of every sidechain. Without one
	// the index still serves its own chain, and the drivechain routes say they
	// have no source.
	var enforcer api.Mainchain
	if cfg.enforcerURL != "" {
		enforcer = mainchain.New(cfg.enforcerURL)
		log.Info("reading the sidechain escrow", "enforcer", cfg.enforcerURL)
	}

	server := &http.Server{
		Addr:              listen,
		Handler:           api.NewServer(stores, index.NewBroadcaster(nodes), enforcer, log).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	serverFailed := make(chan error, 1)
	go func() {
		log.Info("serving the esplora api",
			"chain", cfg.chainName, "network", network,
			"listen", listen, "node", nodeURL)
		if err := server.ListenAndServe(); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			serverFailed <- err
		}
	}()

	select {
	case err := <-serverFailed:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	log.Info("stopping")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("stop the server: %w", err)
	}
	return nil
}

func readConfig() (config, error) {
	var cfg config
	flag.StringVar(&cfg.chainName, "chain", "thunder",
		"which sidechain to index, one of "+fmt.Sprint(chain.Supported()))
	flag.StringVar(&cfg.network, "network", "signet", "signet, regtest, or mainnet")
	flag.StringVar(&cfg.nodeURL, "node-url", "",
		"node JSON-RPC address, default the chain's port for the network")
	flag.StringVar(&cfg.databaseURL, "database-url", os.Getenv("DATABASE_URL"),
		"postgres connection string, default DATABASE_URL")
	flag.StringVar(&cfg.listen, "listen", "",
		"listen address, default the chain's port for the network")
	flag.StringVar(&cfg.enforcerURL, "enforcer-url", "",
		"bip300301 enforcer to read the sidechain escrow from; empty serves no drivechain routes")
	flag.StringVar(&cfg.logLevel, "log-level", "info", "debug, info, warn, or error")
	flag.Parse()

	if cfg.databaseURL == "" {
		return config{}, errors.New("set --database-url or DATABASE_URL")
	}
	return cfg, nil
}

func newLogger(level string) (*slog.Logger, error) {
	var parsed slog.Level
	if err := parsed.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("log level %q is not debug, info, warn, or error", level)
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: parsed})), nil
}
