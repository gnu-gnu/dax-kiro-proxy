package launcher

import (
	"context"
	"errors"
	"sync"
	"time"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/gateway"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/schemacheck"
)

var (
	ErrRunCleanup     = errors.New("launcher could not complete owned runtime cleanup")
	ErrGatewayStopped = errors.New("local gateway stopped before the client exited")
)

type OwnedBackend interface {
	inference.Backend
	Close() error
}

type ClientRunConfig struct {
	Backend  OwnedBackend
	Models   *ModelState
	Schema   *schemacheck.Pool
	Client   ClientConfig
	Server   gateway.ServerConfig
	Attached childproc.AttachedConfig
	IO       childproc.AttachedIO
}

type ClientRunResult struct {
	ClientPID, ExitCode                               int
	GatewayTime, ProfileTime, LaunchTime, CleanupTime time.Duration
}

// RunClient owns Backend, Models, Schema and Server.Gateway.Usage from entry, including every failure path.
// Owners must honor their finite cancellation/Close contracts. Callers perform binary, login and
// restricted-backend preflight before invoking this internal local-client stage. This function does
// not grant execution authority or establish a Kiro policy or client version as verified.
//
// Client routing/auth fields and the server's backend/tokens must be empty: this owner generates the
// two ephemeral credentials and connects its profile to the actual owned loopback listener. It never
// returns credentials or captures client descriptors. The caller must not concurrently reuse owners.
func RunClient(ctx context.Context, cfg ClientRunConfig) (result ClientRunResult, runErr error) {
	result.ExitCode = -1
	owned, cancel := context.WithCancel(ctx)
	var server *gateway.Server
	var attached *childproc.Attached
	var client *childproc.AttachedProcess
	var profile *ClientProfile
	defer func() {
		started := time.Now()
		cancel()
		// Cancel HTTP, suspended backend turns, usage refresh and the attached client together.
		// No open response is required for the backend owner to have pending relay work.
		var jobs sync.WaitGroup
		var serverErr, clientErr, backendErr error
		if server != nil {
			jobs.Go(func() { serverErr = server.Close() })
		}
		if client != nil {
			jobs.Go(func() { clientErr = client.Close() })
		}
		if cfg.Backend != nil {
			jobs.Go(func() { backendErr = cfg.Backend.Close() })
		}
		if cfg.Server.Gateway.Usage != nil {
			jobs.Go(cfg.Server.Gateway.Usage.Close)
		}
		jobs.Wait()
		// A delivered handler may still record its final model while server shutdown joins it.
		if cfg.Models != nil {
			cfg.Models.Close()
		}
		if attached != nil {
			attached.Close()
		}
		if cfg.Schema != nil {
			cfg.Schema.Close()
		}
		if client != nil {
			child, _ := client.Wait()
			result.ExitCode = child.ExitCode
		}
		// Ephemeral settings and credentials outlive their client, HTTP handlers and backend owners.
		var profileErr error
		if profile != nil {
			profileErr = profile.Close()
		}
		if serverErr != nil || backendErr != nil || profileErr != nil || errors.Is(clientErr, childproc.ErrCleanup) || errors.Is(clientErr, childproc.ErrTerminal) {
			// An adapter's arbitrary cleanup error must not expose paths, account data or tool values.
			runErr = errors.Join(runErr, ErrRunCleanup)
		}
		result.CleanupTime = time.Since(started)
	}()
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if cfg.Backend == nil || cfg.Client.GatewayURL != "" || cfg.Client.ModelToken != "" || cfg.Server.Gateway.Backend != nil || cfg.Server.Gateway.Tokens != (gateway.Tokens{}) || cfg.Server.UnsafeNetwork {
		return result, ErrConfig
	}
	if cfg.Models != nil {
		if cfg.Client.Model != "" || !cfg.Models.ready() {
			return result, ErrConfig
		}
		cfg.Client.Model = cfg.Models.Selection().Client
	}
	var err error
	attached, err = childproc.NewAttached(cfg.Attached)
	if err != nil {
		return result, ErrConfig
	}
	tokens, err := gateway.NewTokens()
	if err != nil {
		return result, ErrRuntime
	}
	cfg.Server.Gateway.Tokens, cfg.Server.Gateway.Backend = tokens, cfg.Backend
	if cfg.Models != nil {
		cfg.Server.Gateway.Backend = &catalogBackend{inner: cfg.Backend, models: cfg.Models}
	}
	started := time.Now()
	server, err = gateway.StartServer(owned, cfg.Server)
	result.GatewayTime = time.Since(started)
	if err != nil {
		return result, err
	}
	cfg.Client.GatewayURL, cfg.Client.ModelToken = server.URL(), tokens.Model
	started = time.Now()
	profile, err = PrepareClient(cfg.Client)
	result.ProfileTime = time.Since(started)
	if err != nil {
		return result, err
	}
	started = time.Now()
	client, err = attached.Start(owned, profile.Command(), cfg.IO)
	result.LaunchTime = time.Since(started)
	if err != nil {
		return result, err
	}
	result.ClientPID = client.PID()
	select {
	case <-ctx.Done():
		return result, ctx.Err()
	case <-client.Done():
		child, err := client.Wait()
		result.ExitCode = child.ExitCode
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return result, err
	case <-server.Done():
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if err := server.Wait(); err != nil {
			return result, err
		}
		return result, ErrGatewayStopped
	}
}
