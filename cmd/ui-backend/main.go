package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	"github.com/ZeljkoBenovic/mikrotik-operator/internal/uiapi"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/validation"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	localStaticDir     = "./web/ui/dist"
	containerStaticDir = "/ui"
	shutdownTimeout    = 10 * time.Second
)

type config struct {
	bindAddress string
	kubeconfig  string
	staticDir   string
	namespace   string
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	cfg, err := parseFlags(args)
	if err != nil {
		return 2
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	restCfg, err := loadRESTConfig(cfg.kubeconfig)
	if err != nil {
		log.Error("load kubernetes config", "err", err)
		return 1
	}

	kube, err := client.New(restCfg, client.Options{Scheme: newScheme()})
	if err != nil {
		log.Error("create kubernetes client", "err", err)
		return 1
	}

	namespace := operatorNamespace(cfg.namespace)
	if len(validation.IsDNS1123Subdomain(namespace)) != 0 {
		log.Error("invalid operator namespace", "namespace", namespace)
		return 2
	}

	srv := &http.Server{
		Addr: cfg.bindAddress,
		Handler: uiapi.New(uiapi.Options{
			Client:    kube,
			Logger:    log,
			StaticDir: cfg.staticDir,
			Namespace: namespace,
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("ui backend listening", "addr", cfg.bindAddress, "staticDir", cfg.staticDir)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Error("http server", "err", err)
			return 1
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Error("http shutdown", "err", err)
			return 1
		}
	}
	return 0
}

func parseFlags(args []string) (config, error) {
	fs := flag.NewFlagSet("ui-backend", flag.ContinueOnError)
	cfg := config{}
	fs.StringVar(&cfg.bindAddress, "bind-address", ":8080", "HTTP bind address")
	fs.StringVar(&cfg.kubeconfig, "kubeconfig", "", "Path to kubeconfig; empty uses in-cluster config")
	fs.StringVar(&cfg.staticDir, "static-dir", defaultStaticDir(), "Directory of built SPA files")
	fs.StringVar(&cfg.namespace, "namespace", "", "Operator namespace used for UI-created resources")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	return cfg, nil
}

func operatorNamespace(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if ns := os.Getenv("POD_NAMESPACE"); ns != "" {
		return ns
	}
	return "default"
}

func defaultStaticDir() string {
	if st, err := os.Stat(localStaticDir); err == nil && st.IsDir() {
		return localStaticDir
	}
	if st, err := os.Stat(containerStaticDir); err == nil && st.IsDir() {
		return containerStaticDir
	}
	return localStaticDir
}

func loadRESTConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("load kubeconfig: %w", err)
		}
		return cfg, nil
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("in-cluster config: %w", err)
	}
	return cfg, nil
}

func newScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(api.AddToScheme(scheme))
	return scheme
}
