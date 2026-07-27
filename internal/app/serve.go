package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"charm.land/log/v2"
	"github.com/jmcampanini/gibson/internal/config"
	"github.com/jmcampanini/gibson/internal/httpapi"
	"github.com/jmcampanini/gibson/internal/pisession"
	"github.com/jmcampanini/gibson/internal/store"
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
	assets         fs.FS
	getwd          func() (string, error)
	listen         func(network, address string) (net.Listener, error)
	checkIgnored   func(checkoutRoot string) (bool, error)
	resolvePiBin   func(configured string) (string, error)
	checkPiVersion func(context.Context, string) (pisession.VersionResult, error)
}

func Serve(ctx context.Context, options ServeOptions, logger *log.Logger) error {
	return serve(ctx, options, logger, serveDependencies{
		assets:         web.Dist,
		getwd:          os.Getwd,
		listen:         net.Listen,
		checkIgnored:   store.CheckIgnored,
		resolvePiBin:   pisession.ResolvePiBin,
		checkPiVersion: pisession.CheckPiVersion,
	})
}

func serve(ctx context.Context, options ServeOptions, logger *log.Logger, dependencies serveDependencies) error {
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

	loaded, err := config.Load(ws.LaunchCheckout)
	if err != nil {
		return err
	}
	cfg := loaded.Config

	port := cfg.Server.Port
	if options.PortOverride != nil {
		port = *options.PortOverride
	}

	if len(loaded.UnknownKeys) > 0 {
		logger.Warn("unknown configuration keys", "keys", strings.Join(loaded.UnknownKeys, ", "))
	}
	if len(cfg.Sessions) == 0 {
		logger.Warn("no session types configured", "config", filepath.Join(ws.LaunchCheckout, "gibson.toml"))
	}
	ignored, err := dependencies.checkIgnored(ws.LaunchCheckout)
	if err != nil {
		return err
	}
	if !ignored {
		logger.Warn(".gibson/ is not ignored", "fix", "add .gibson/ to "+filepath.Join(ws.LaunchCheckout, ".gitignore"))
	}

	piBin, err := dependencies.resolvePiBin(cfg.Server.PiBin)
	if err != nil {
		return err
	}
	piVersion, err := dependencies.checkPiVersion(ctx, piBin)
	if err != nil {
		return err
	}
	if !piVersion.Verified {
		logger.Warn(
			"pi version has not been verified with Gibson",
			"found", piVersion.Found,
			"verified", fmt.Sprintf("0.%d.x", pisession.VerifiedPiMinor),
			"minimum", pisession.MinimumPiVersion,
		)
	}

	static, err := fs.Sub(dependencies.assets, "dist")
	if err != nil {
		return embeddedAssetsError(err)
	}
	handler, err := httpapi.New(httpapi.Options{
		StaticFS: static,
		Version:  options.Version,
	})
	if err != nil {
		return embeddedAssetsError(err)
	}

	address := net.JoinHostPort(cfg.Server.Bind, strconv.Itoa(port))
	listener, err := dependencies.listen("tcp", address)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return fmt.Errorf("listen on %s: port is already in use; change server.port in gibson.toml or pass --port: %w", address, err)
		}
		return fmt.Errorf("listen on %s: %w", address, err)
	}
	defer func() { _ = listener.Close() }()

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ErrorLog: logger.StandardLog(log.StandardLogOptions{
			ForceLevel: log.ErrorLevel,
		}),
	}
	logger.Info(
		"server started",
		"workspace", ws.Root,
		"checkout", ws.LaunchCheckout,
		"url", serverURL(cfg.Server.Bind, listener.Addr()),
		"dev", options.Dev,
		"pi_version", piVersion.Found,
	)
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

func embeddedAssetsError(err error) error {
	return fmt.Errorf("embedded web assets are not ready; rebuild with 'make build': %w", err)
}

func serverURL(bind string, address net.Addr) string {
	port := ""
	if _, found, err := net.SplitHostPort(address.String()); err == nil {
		port = found
	}
	if port == "" {
		return "http://" + address.String()
	}
	return "http://" + net.JoinHostPort(bind, port)
}
