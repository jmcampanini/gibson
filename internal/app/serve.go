package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/jmcampanini/gibson/internal/config"
	"github.com/jmcampanini/gibson/internal/httpapi"
	"github.com/jmcampanini/gibson/internal/workspace"
	"github.com/jmcampanini/gibson/web"
)

const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 5 * time.Second
)

type ServeOptions struct {
	PortOverride *int
	Dev          bool
	Version      string
}

type serveDependencies struct {
	assets fs.FS
	getwd  func() (string, error)
	listen func(network, address string) (net.Listener, error)
}

func Serve(ctx context.Context, options ServeOptions) error {
	return serve(ctx, options, serveDependencies{
		assets: web.Dist,
		getwd:  os.Getwd,
		listen: net.Listen,
	})
}

func serve(ctx context.Context, options ServeOptions, dependencies serveDependencies) error {
	if options.Dev {
		return errors.New("development serving is not available yet")
	}

	workingDirectory, err := dependencies.getwd()
	if err != nil {
		return fmt.Errorf("determine working directory: %w", err)
	}

	ws, err := workspace.Locate(workingDirectory)
	if err != nil {
		return err
	}

	cfg, err := config.Load(ws.LaunchCheckout)
	if err != nil {
		return err
	}

	port := cfg.Server.Port
	if options.PortOverride != nil {
		port = *options.PortOverride
	}

	static, err := fs.Sub(dependencies.assets, "dist")
	if err != nil {
		return fmt.Errorf("load embedded web assets: %w", err)
	}
	handler, err := httpapi.New(httpapi.Options{
		StaticFS: static,
		Version:  options.Version,
	})
	if err != nil {
		return err
	}

	address := net.JoinHostPort(cfg.Server.Bind, strconv.Itoa(port))
	listener, err := dependencies.listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", address, err)
	}
	defer func() { _ = listener.Close() }()

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
	}
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- server.Serve(listener)
	}()

	select {
	case err := <-serveResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return fmt.Errorf("shut down HTTP server: %w", err)
		}
		if err := <-serveResult; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	}
}
