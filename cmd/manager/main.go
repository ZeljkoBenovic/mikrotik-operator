package main

import (
	"flag"
	"os"

	api "github.com/ZeljkoBenovic/mikrotik-operator/api/v1alpha1"
	"github.com/ZeljkoBenovic/mikrotik-operator/internal/controller"
	"github.com/ZeljkoBenovic/mikrotik-operator/internal/routeros"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func main() {
	var metricsAddr, probeAddr string
	var leaderElect, gatewayAPIEnabled bool
	gatewayClass := api.GatewayClassName
	gatewayController := api.GatewayController
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "metrics address")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "health address")
	flag.BoolVar(&leaderElect, "leader-elect", false, "Enable leader election for controller manager")
	flag.BoolVar(
		&gatewayAPIEnabled,
		"gateway-api-enabled",
		false,
		"Enable Gateway API HTTPRoute support; requires Gateway API CRDs",
	)
	flag.StringVar(&gatewayClass, "gateway-class-name", gatewayClass, "GatewayClass name handled by the operator")
	flag.StringVar(&gatewayController, "gateway-controller-name", gatewayController, "GatewayClass controller name handled by the operator")
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zap.Options{Development: false})))
	s := runtime.NewScheme()
	utilruntime.Must(scheme.AddToScheme(s))
	utilruntime.Must(networkingv1.AddToScheme(s))
	if gatewayAPIEnabled {
		utilruntime.Must(gatewayv1.Install(s))
	}
	utilruntime.Must(api.AddToScheme(s))
	mgr, err := ctrl.NewManager(
		ctrl.GetConfigOrDie(),
		ctrl.Options{
			Scheme:                 s,
			Metrics:                metricsserver.Options{BindAddress: metricsAddr},
			HealthProbeBindAddress: probeAddr,
			LeaderElection:         leaderElect,
			LeaderElectionID:       "mikrotik-operator",
		},
	)
	if err != nil {
		os.Exit(1)
	}
	if err = controller.Setup(mgr, routeros.Dial, gatewayAPIEnabled, gatewayClass, gatewayController); err != nil {
		os.Exit(1)
	}
	if err = mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		os.Exit(1)
	}
	if err = mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		os.Exit(1)
	}
	if err = mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		os.Exit(1)
	}
}
