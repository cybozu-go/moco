package pprof

import (
	"context"
	"fmt"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type Handler struct {
	log      logr.Logger
	bindAddr string
}

var (
	_ manager.Runnable               = &Handler{}
	_ manager.LeaderElectionRunnable = &Handler{}
)

func NewHandler(log logr.Logger, bindAddr string) *Handler {
	return &Handler{
		log:      log,
		bindAddr: bindAddr,
	}
}

func (h *Handler) NeedLeaderElection() bool {
	return false
}

func (h *Handler) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	server := &http.Server{Addr: h.bindAddr, Handler: mux}
	errCh := make(chan error)
	h.log.Info("starting handler", "addr", h.bindAddr)
	go func() {
		errCh <- server.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		// ListenAndServe always returns a non-nil error. So no need for a nil check.
		h.log.Error(err, "failed to listen")
		return fmt.Errorf("failed to listen: %w", err)
	case <-ctx.Done():
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			h.log.Error(err, "failed to shutdown")
			return fmt.Errorf("failed to shutdown: %v", err)
		}
	}

	h.log.Info("shutdown")
	return nil
}
