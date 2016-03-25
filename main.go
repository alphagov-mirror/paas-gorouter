package main

import (
	"crypto/tls"
	"errors"

	"github.com/apcera/nats"
	cf_debug_server "github.com/cloudfoundry-incubator/cf-debug-server"
	"github.com/cloudfoundry-incubator/cf-lager"
	routing_api "github.com/cloudfoundry-incubator/routing-api"
	uaa_client "github.com/cloudfoundry-incubator/uaa-go-client"
	uaa_config "github.com/cloudfoundry-incubator/uaa-go-client/config"
	"github.com/cloudfoundry/dropsonde"
	"github.com/cloudfoundry/gorouter/access_log"
	vcap "github.com/cloudfoundry/gorouter/common"
	"github.com/cloudfoundry/gorouter/common/secure"
	"github.com/cloudfoundry/gorouter/config"
	"github.com/cloudfoundry/gorouter/proxy"
	rregistry "github.com/cloudfoundry/gorouter/registry"
	"github.com/cloudfoundry/gorouter/route_fetcher"
	"github.com/cloudfoundry/gorouter/router"
	rvarz "github.com/cloudfoundry/gorouter/varz"
	steno "github.com/cloudfoundry/gosteno"
	"github.com/cloudfoundry/yagnats"
	"github.com/pivotal-golang/clock"
	"github.com/pivotal-golang/lager"

	"flag"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"time"

	"github.com/cloudfoundry/gorouter/metrics"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/grouper"
	"github.com/tedsuo/ifrit/sigmon"
)

var configFile string

func main() {
	flag.StringVar(&configFile, "c", "", "Configuration File")
	cf_lager.AddFlags(flag.CommandLine)
	flag.Parse()

	c := config.DefaultConfig()
	logCounter := vcap.NewLogCounter()

	if configFile != "" {
		c = config.InitConfigFromFile(configFile)
	}

	InitLoggerFromConfig(c, logCounter)
	logger, _ := cf_lager.New("router.main")

	err := dropsonde.Initialize(c.Logging.MetronAddress, c.Logging.JobName)
	if err != nil {
		logger.Error("Dropsonde failed to initialize ", err)
		os.Exit(1)
	}

	// setup number of procs
	if c.GoMaxProcs != 0 {
		runtime.GOMAXPROCS(c.GoMaxProcs)
	}

	if c.DebugAddr != "" {
		cf_debug_server.Run(c.DebugAddr)
	}

	logger.Info("Setting up NATs connection")
	natsClient := connectToNatsServer(logger, c)

	metricsReporter := metrics.NewMetricsReporter()
	registry := rregistry.NewRouteRegistry(c, natsClient, metricsReporter)

	varz := rvarz.NewVarz(registry)
	compositeReporter := metrics.NewCompositeReporter(varz, metricsReporter)

	accessLogger, err := access_log.CreateRunningAccessLogger(c)
	if err != nil {
		logger.Fatal("Error creating access logger: ", err)
	}

	var crypto secure.Crypto
	var cryptoPrev secure.Crypto
	if c.RouteServiceEnabled {
		crypto = createCrypto(logger, c.RouteServiceSecret)
		if c.RouteServiceSecretPrev != "" {
			cryptoPrev = createCrypto(logger, c.RouteServiceSecretPrev)
		}
	}

	proxy := buildProxy(logger, c, registry, accessLogger, compositeReporter, crypto, cryptoPrev)

	router, err := router.NewRouter(c, proxy, natsClient, registry, varz, logCounter, nil)
	if err != nil {
		logger.Error("An error occurred: ", err)
		os.Exit(1)
	}

	members := grouper.Members{
		{"router", router},
	}
	if c.RoutingApiEnabled() {
		logger.Info("Setting up route fetcher")
		routeFetcher := setupRouteFetcher(logger, c, registry)

		// check connectivity to routing api
		err := routeFetcher.FetchRoutes()
		if err != nil {
			logger.Error("Failed to connect to the Routing API: %s\n", err)
			os.Exit(1)
		}
		members = append(members, grouper.Member{"router-fetcher", routeFetcher})
	}

	group := grouper.NewOrdered(os.Interrupt, members)

	monitor := ifrit.Invoke(sigmon.New(group, syscall.SIGTERM, syscall.SIGINT, syscall.SIGUSR1))

	err = <-monitor.Wait()
	if err != nil {
		logger.Error("gorouter.exited-with-failure: ", err)
		os.Exit(1)
	}

	os.Exit(0)
}

func createCrypto(logger lager.Logger, secret string) *secure.AesGCM {
	// generate secure encryption key using key derivation function (pbkdf2)
	secretPbkdf2 := secure.NewPbkdf2([]byte(secret), 16)
	crypto, err := secure.NewAesGCM(secretPbkdf2)
	if err != nil {
		logger.Error("Error creating route service crypto: %s\n", err)
		os.Exit(1)
	}
	return crypto
}

func buildProxy(logger lager.Logger, c *config.Config, registry rregistry.RegistryInterface, accessLogger access_log.AccessLogger, reporter metrics.ProxyReporter, crypto secure.Crypto, cryptoPrev secure.Crypto) proxy.Proxy {
	args := proxy.ProxyArgs{
		EndpointTimeout: c.EndpointTimeout,
		Ip:              c.Ip,
		TraceKey:        c.TraceKey,
		Registry:        registry,
		Reporter:        reporter,
		AccessLogger:    accessLogger,
		SecureCookies:   c.SecureCookies,
		TLSConfig: &tls.Config{
			CipherSuites:       c.CipherSuites,
			InsecureSkipVerify: c.SSLSkipValidation,
		},
		RouteServiceEnabled:        c.RouteServiceEnabled,
		RouteServiceTimeout:        c.RouteServiceTimeout,
		RouteServiceRecommendHttps: c.RouteServiceRecommendHttps,
		Crypto:            crypto,
		CryptoPrev:        cryptoPrev,
		ExtraHeadersToLog: c.ExtraHeadersToLog,
		Logger:            logger,
	}
	return proxy.NewProxy(args)
}

func setupRouteFetcher(logger lager.Logger, c *config.Config, registry rregistry.RegistryInterface) *route_fetcher.RouteFetcher {
	clock := clock.NewClock()

	uaaClient := newUaaClient(logger, clock, c)

	_, err := uaaClient.FetchToken(false)
	if err != nil {
		logger.Error("Unable to fetch token: ", err)
		os.Exit(1)
	}

	routingApiUri := fmt.Sprintf("%s:%d", c.RoutingApi.Uri, c.RoutingApi.Port)
	routingApiClient := routing_api.NewClient(routingApiUri)

	routeFetcher := route_fetcher.NewRouteFetcher(logger, uaaClient, registry, c, routingApiClient, 1, clock)

	return routeFetcher
}

func newUaaClient(logger lager.Logger, clock clock.Clock, c *config.Config) uaa_client.Client {
	if c.RoutingApi.AuthDisabled {
		logger.Info("using-noop-token-fetcher")
		return uaa_client.NewNoOpUaaClient()
	}

	if c.OAuth.Port == -1 {
		logger.Fatal("tls-not-enabled", errors.New("GoRouter requires TLS enabled to get OAuth token"), lager.Data{"token-endpoint": c.OAuth.TokenEndpoint, "port": c.OAuth.Port})
	}

	tokenURL := fmt.Sprintf("https://%s:%d", c.OAuth.TokenEndpoint, c.OAuth.Port)

	cfg := &uaa_config.Config{
		UaaEndpoint:           tokenURL,
		SkipVerification:      c.OAuth.SkipOAuthTLSVerification,
		ClientName:            c.OAuth.ClientName,
		ClientSecret:          c.OAuth.ClientSecret,
		MaxNumberOfRetries:    c.TokenFetcherMaxRetries,
		RetryInterval:         c.TokenFetcherRetryInterval,
		ExpirationBufferInSec: c.TokenFetcherExpirationBufferTimeInSeconds,
	}

	uaaClient, err := uaa_client.NewClient(logger, cfg, clock)
	if err != nil {
		logger.Error("Error creating token fetcher: %s\n", err)
		os.Exit(1)
	}
	return uaaClient
}

func connectToNatsServer(logger lager.Logger, c *config.Config) yagnats.NATSConn {
	var natsClient yagnats.NATSConn
	var err error

	natsServers := c.NatsServers()
	attempts := 3
	for attempts > 0 {
		natsClient, err = yagnats.Connect(natsServers)
		if err == nil {
			break
		} else {
			attempts--
			time.Sleep(100 * time.Millisecond)
		}
	}

	if err != nil {
		logger.Error("Error connecting to NATS: %s\n", err)
		os.Exit(1)
	}

	natsClient.AddClosedCB(func(conn *nats.Conn) {
		logger.Error("Close on NATS client. nats.Conn: ", err, lager.Data{"connection": *conn})
		os.Exit(1)
	})

	return natsClient
}

func InitLoggerFromConfig(c *config.Config, logCounter *vcap.LogCounter) {
	l, err := steno.GetLogLevel(c.Logging.Level)
	if err != nil {
		panic(err)
	}

	s := make([]steno.Sink, 0, 3)
	if c.Logging.File != "" {
		s = append(s, steno.NewFileSink(c.Logging.File))
	} else {
		s = append(s, steno.NewIOSink(os.Stdout))
	}

	if c.Logging.Syslog != "" {
		s = append(s, steno.NewSyslogSink(c.Logging.Syslog))
	}

	s = append(s, logCounter)

	stenoConfig := &steno.Config{
		Sinks: s,
		Codec: steno.NewJsonCodec(),
		Level: l,
	}

	steno.Init(stenoConfig)
}
