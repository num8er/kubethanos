package main

import (
	"context"
	"io/ioutil"
	"kubethanos/kubethanos"
	"kubethanos/thanos"
	"math/rand"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/alecthomas/kingpin"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"

	log "github.com/sirupsen/logrus"
)

var (
	namespaces       string
	includedPodNames *regexp.Regexp
	excludedPodNames *regexp.Regexp
	master           string
	kubeconfig       string
	interval         time.Duration
	dryRun           bool
	debug            bool
)

func init() {
	rand.Seed(time.Now().UTC().UnixNano())
	klog.SetOutput(ioutil.Discard)

	kingpin.Flag("namespaces", "A namespace or a set of namespaces to restrict thanoskube").StringVar(&namespaces)
	kingpin.Flag("included-pod-names", "A regex to select which pods to kill").RegexpVar(&includedPodNames)
	kingpin.Flag("excluded-pod-names", "A regex to exclude pods to kill").RegexpVar(&excludedPodNames)
	kingpin.Flag("master", "The address of the Kubernetes cluster to target, if none looks under $HOME/.kube").StringVar(&master)
	kingpin.Flag("kubeconfig", "Path to a kubeconfig file").StringVar(&kubeconfig)
	kingpin.Flag("interval", "Interval between killing pods").Default("10m").DurationVar(&interval)
	kingpin.Flag("dry-run", "If true, print out the pod names without actually killing them.").Default("true").BoolVar(&dryRun)
	kingpin.Flag("debug", "Enable debug logging.").BoolVar(&debug)
}

func main() {
	kingpin.Parse()

	if debug {
		log.SetLevel(log.DebugLevel)
	}

	log.WithFields(log.Fields{
		"namespaces":       namespaces,
		"includedPodNames": includedPodNames,
		"excludedPodNames": excludedPodNames,
		"master":           master,
		"kubeconfig":       kubeconfig,
		"interval":         interval,
		"dryRun":           dryRun,
		"debug":            debug,
	}).Debug("reading config")

	log.WithFields(log.Fields{
		"dryRun":   dryRun,
		"interval": interval,
	}).Info("starting up")

	client, err := newClient()
	if err != nil {
		log.WithField("err", err).Fatal("failed to connect to cluster")
	}

	var (
		namespaces = parseSelector(namespaces)
	)

	log.WithFields(log.Fields{
		"namespaces":       namespaces,
		"includedPodNames": includedPodNames,
		"excludedPodNames": excludedPodNames,
	}).Info("setting pod filter")

	kubeThanos := kubethanos.New(
		client,
		namespaces,
		includedPodNames,
		excludedPodNames,
		dryRun,
		thanos.NewThanos(client, log.StandardLogger()),
	)

	done := make(chan os.Signal, 1)
	signal.Notify(done, syscall.SIGINT, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-done
		cancel()
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	kubeThanos.Run(ctx, ticker.C)
}

func newClient() (*kubernetes.Clientset, error) {
	if kubeconfig == "" {
		if _, err := os.Stat(clientcmd.RecommendedHomeFile); err == nil {
			kubeconfig = clientcmd.RecommendedHomeFile
		}
	}

	log.WithFields(log.Fields{
		"kubeconfig": kubeconfig,
		"master":     master,
	}).Debug("using cluster config")

	config, err := clientcmd.BuildConfigFromFlags(master, kubeconfig)
	if err != nil {
		return nil, err
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	serverVersion, err := client.Discovery().ServerVersion()
	if err != nil {
		return nil, err
	}

	log.WithFields(log.Fields{
		"master":        config.Host,
		"serverVersion": serverVersion,
	}).Info("connected to cluster")

	return client, nil
}

func parseSelector(str string) labels.Selector {
	selector, err := labels.Parse(str)
	if err != nil {
		log.WithFields(log.Fields{
			"selector": str,
			"err":      err,
		}).Fatal("failed to parse selector")
	}
	return selector
}