package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof" // Comment this line to disable pprof endpoint.
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	conntrack "github.com/mwitkow/go-conntrack"
	"github.com/oklog/run"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/model"
	"github.com/prometheus/common/promlog"
	promlogflag "github.com/prometheus/common/promlog/flag"
	"github.com/prometheus/common/version"
	"github.com/prometheus/prometheus/config"
	"github.com/prometheus/prometheus/pkg/relabel"
	prom_runtime "github.com/prometheus/prometheus/pkg/runtime"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/storage/remote"
	"github.com/prometheus/prometheus/storage/tsdb"
	"github.com/shynome/pprometheus/web"
	kingpin "gopkg.in/alecthomas/kingpin.v2"
	"k8s.io/klog"
)

var (
	configSuccess = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "prometheus_config_last_reload_successful",
		Help: "Whether the last configuration reload attempt was successful.",
	})
	configSuccessTime = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "prometheus_config_last_reload_success_timestamp_seconds",
		Help: "Timestamp of the last successful configuration reload.",
	})

	defaultRetentionString   = "15d"
	defaultRetentionDuration model.Duration
)

func init() {
	prometheus.MustRegister(version.NewCollector("prometheus"))

	var err error
	defaultRetentionDuration, err = model.ParseDuration(defaultRetentionString)
	if err != nil {
		panic(err)
	}
}

func main() {
	if os.Getenv("DEBUG") != "" {
		runtime.SetBlockProfileRate(20)
		runtime.SetMutexProfileFraction(20)
	}

	var (
		oldFlagRetentionDuration model.Duration
		newFlagRetentionDuration model.Duration
	)

	cfg := struct {
		configFile string

		localStoragePath    string
		forGracePeriod      model.Duration
		outageTolerance     model.Duration
		resendDelay         model.Duration
		web                 web.Options
		tsdb                tsdb.Options
		lookbackDelta       model.Duration
		webTimeout          model.Duration
		queryTimeout        model.Duration
		queryConcurrency    int
		queryMaxSamples     int
		RemoteFlushDeadline model.Duration

		prometheusURL   string
		corsRegexString string

		promlogConfig promlog.Config
	}{
		web: web.Options{
			Registerer: prometheus.DefaultRegisterer,
			Gatherer:   prometheus.DefaultGatherer,
		},
		promlogConfig: promlog.Config{},
	}

	a := kingpin.New(filepath.Base(os.Args[0]), "The Prometheus monitoring server")

	a.Version(version.Print("prometheus"))

	a.HelpFlag.Short('h')

	a.Flag("config.file", "Prometheus configuration file path.").
		Default("prometheus.yml").StringVar(&cfg.configFile)

	a.Flag("web.listen-address", "Address to listen on for UI, API, and telemetry.").
		Default("0.0.0.0:9090").StringVar(&cfg.web.ListenAddress)

	a.Flag("web.read-timeout",
		"Maximum duration before timing out read of the request, and closing idle connections.").
		Default("5m").SetValue(&cfg.webTimeout)

	a.Flag("web.max-connections", "Maximum number of simultaneous connections.").
		Default("512").IntVar(&cfg.web.MaxConnections)

	a.Flag("web.external-url",
		"The URL under which Prometheus is externally reachable (for example, if Prometheus is served via a reverse proxy). Used for generating relative and absolute links back to Prometheus itself. If the URL has a path portion, it will be used to prefix all HTTP endpoints served by Prometheus. If omitted, relevant URL components will be derived automatically.").
		PlaceHolder("<URL>").StringVar(&cfg.prometheusURL)

	a.Flag("web.route-prefix",
		"Prefix for the internal routes of web endpoints. Defaults to path of --web.external-url.").
		PlaceHolder("<path>").StringVar(&cfg.web.RoutePrefix)

	a.Flag("web.user-assets", "Path to static asset directory, available at /user.").
		PlaceHolder("<path>").StringVar(&cfg.web.UserAssetsPath)

	a.Flag("web.enable-lifecycle", "Enable shutdown and reload via HTTP request.").
		Default("false").BoolVar(&cfg.web.EnableLifecycle)

	a.Flag("web.enable-admin-api", "Enable API endpoints for admin control actions.").
		Default("false").BoolVar(&cfg.web.EnableAdminAPI)

	a.Flag("web.console.templates", "Path to the console template directory, available at /consoles.").
		Default("consoles").StringVar(&cfg.web.ConsoleTemplatesPath)

	a.Flag("web.console.libraries", "Path to the console library directory.").
		Default("console_libraries").StringVar(&cfg.web.ConsoleLibrariesPath)

	a.Flag("web.page-title", "Document title of Prometheus instance.").
		Default("Prometheus Time Series Collection and Processing Server").StringVar(&cfg.web.PageTitle)

	a.Flag("web.cors.origin", `Regex for CORS origin. It is fully anchored. Example: 'https?://(domain1|domain2)\.com'`).
		Default(".*").StringVar(&cfg.corsRegexString)

	a.Flag("storage.tsdb.path", "Base path for metrics storage.").
		Default("data/").StringVar(&cfg.localStoragePath)

	a.Flag("storage.tsdb.min-block-duration", "Minimum duration of a data block before being persisted. For use in testing.").
		Hidden().Default("2h").SetValue(&cfg.tsdb.MinBlockDuration)

	a.Flag("storage.tsdb.max-block-duration",
		"Maximum duration compacted blocks may span. For use in testing. (Defaults to 10% of the retention period.)").
		Hidden().PlaceHolder("<duration>").SetValue(&cfg.tsdb.MaxBlockDuration)

	a.Flag("storage.tsdb.wal-segment-size",
		"Size at which to split the tsdb WAL segment files. Example: 100MB").
		Hidden().PlaceHolder("<bytes>").BytesVar(&cfg.tsdb.WALSegmentSize)

	a.Flag("storage.tsdb.retention", "[DEPRECATED] How long to retain samples in storage. This flag has been deprecated, use \"storage.tsdb.retention.time\" instead.").
		SetValue(&oldFlagRetentionDuration)

	a.Flag("storage.tsdb.retention.time", "How long to retain samples in storage. When this flag is set it overrides \"storage.tsdb.retention\". If neither this flag nor \"storage.tsdb.retention\" nor \"storage.tsdb.retention.size\" is set, the retention time defaults to "+defaultRetentionString+". Units Supported: y, w, d, h, m, s, ms.").
		SetValue(&newFlagRetentionDuration)

	a.Flag("storage.tsdb.retention.size", "[EXPERIMENTAL] Maximum number of bytes that can be stored for blocks. Units supported: KB, MB, GB, TB, PB. This flag is experimental and can be changed in future releases.").
		BytesVar(&cfg.tsdb.MaxBytes)

	a.Flag("storage.tsdb.no-lockfile", "Do not create lockfile in data directory.").
		Default("false").BoolVar(&cfg.tsdb.NoLockfile)

	a.Flag("storage.tsdb.allow-overlapping-blocks", "[EXPERIMENTAL] Allow overlapping blocks, which in turn enables vertical compaction and vertical query merge.").
		Default("false").BoolVar(&cfg.tsdb.AllowOverlappingBlocks)

	a.Flag("storage.tsdb.wal-compression", "Compress the tsdb WAL.").
		Default("false").BoolVar(&cfg.tsdb.WALCompression)

	a.Flag("storage.remote.flush-deadline", "How long to wait flushing sample on shutdown or config reload.").
		Default("1m").PlaceHolder("<duration>").SetValue(&cfg.RemoteFlushDeadline)

	a.Flag("storage.remote.read-sample-limit", "Maximum overall number of samples to return via the remote read interface, in a single query. 0 means no limit. This limit is ignored for streamed response types.").
		Default("5e7").IntVar(&cfg.web.RemoteReadSampleLimit)

	a.Flag("storage.remote.read-concurrent-limit", "Maximum number of concurrent remote read calls. 0 means no limit.").
		Default("10").IntVar(&cfg.web.RemoteReadConcurrencyLimit)

	a.Flag("storage.remote.read-max-bytes-in-frame", "Maximum number of bytes in a single frame for streaming remote read response types before marshalling. Note that client might have limit on frame size as well. 1MB as recommended by protobuf by default.").
		Default("1048576").IntVar(&cfg.web.RemoteReadBytesInFrame)

	a.Flag("rules.alert.for-outage-tolerance", "Max time to tolerate prometheus outage for restoring \"for\" state of alert.").
		Default("1h").SetValue(&cfg.outageTolerance)

	a.Flag("rules.alert.for-grace-period", "Minimum duration between alert and restored \"for\" state. This is maintained only for alerts with configured \"for\" time greater than grace period.").
		Default("10m").SetValue(&cfg.forGracePeriod)

	a.Flag("rules.alert.resend-delay", "Minimum amount of time to wait before resending an alert to Alertmanager.").
		Default("1m").SetValue(&cfg.resendDelay)

	a.Flag("query.lookback-delta", "The maximum lookback duration for retrieving metrics during expression evaluations.").
		Default("5m").SetValue(&cfg.lookbackDelta)

	a.Flag("query.timeout", "Maximum time a query may take before being aborted.").
		Default("2m").SetValue(&cfg.queryTimeout)

	a.Flag("query.max-concurrency", "Maximum number of queries executed concurrently.").
		Default("20").IntVar(&cfg.queryConcurrency)

	a.Flag("query.max-samples", "Maximum number of samples a single query can load into memory. Note that queries will fail if they try to load more samples than this into memory, so this also limits the number of samples a query can return.").
		Default("50000000").IntVar(&cfg.queryMaxSamples)

	promlogflag.AddFlags(a, &cfg.promlogConfig)

	_, err := a.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, errors.Wrapf(err, "Error parsing commandline arguments"))
		a.Usage(os.Args[1:])
		os.Exit(2)
	}

	logger := promlog.New(&cfg.promlogConfig)

	cfg.web.ExternalURL, err = computeExternalURL(cfg.prometheusURL, cfg.web.ListenAddress)
	if err != nil {
		fmt.Fprintln(os.Stderr, errors.Wrapf(err, "parse external URL %q", cfg.prometheusURL))
		os.Exit(2)
	}

	cfg.web.CORSOrigin, err = compileCORSRegexString(cfg.corsRegexString)
	if err != nil {
		fmt.Fprintln(os.Stderr, errors.Wrapf(err, "could not compile CORS regex string %q", cfg.corsRegexString))
		os.Exit(2)
	}

	cfg.web.ReadTimeout = time.Duration(cfg.webTimeout)
	// Default -web.route-prefix to path of -web.external-url.
	if cfg.web.RoutePrefix == "" {
		cfg.web.RoutePrefix = cfg.web.ExternalURL.Path
	}
	// RoutePrefix must always be at least '/'.
	cfg.web.RoutePrefix = "/" + strings.Trim(cfg.web.RoutePrefix, "/")

	{ // Time retention settings.
		if oldFlagRetentionDuration != 0 {
			level.Warn(logger).Log("deprecation_notice", "'storage.tsdb.retention' flag is deprecated use 'storage.tsdb.retention.time' instead.")
			cfg.tsdb.RetentionDuration = oldFlagRetentionDuration
		}

		// When the new flag is set it takes precedence.
		if newFlagRetentionDuration != 0 {
			cfg.tsdb.RetentionDuration = newFlagRetentionDuration
		}

		if cfg.tsdb.RetentionDuration == 0 && cfg.tsdb.MaxBytes == 0 {
			cfg.tsdb.RetentionDuration = defaultRetentionDuration
			level.Info(logger).Log("msg", "no time or size retention was set so using the default time retention", "duration", defaultRetentionDuration)
		}

		// Check for overflows. This limits our max retention to 100y.
		if cfg.tsdb.RetentionDuration < 0 {
			y, err := model.ParseDuration("100y")
			if err != nil {
				panic(err)
			}
			cfg.tsdb.RetentionDuration = y
			level.Warn(logger).Log("msg", "time retention value is too high. Limiting to: "+y.String())
		}
	}

	{ // Max block size  settings.
		if cfg.tsdb.MaxBlockDuration == 0 {
			maxBlockDuration, err := model.ParseDuration("31d")
			if err != nil {
				panic(err)
			}
			// When the time retention is set and not too big use to define the max block duration.
			if cfg.tsdb.RetentionDuration != 0 && cfg.tsdb.RetentionDuration/10 < maxBlockDuration {
				maxBlockDuration = cfg.tsdb.RetentionDuration / 10
			}

			cfg.tsdb.MaxBlockDuration = maxBlockDuration
		}
	}

	promql.LookbackDelta = time.Duration(cfg.lookbackDelta)
	promql.SetDefaultEvaluationInterval(time.Duration(config.DefaultGlobalConfig.EvaluationInterval))

	// Above level 6, the k8s client would log bearer tokens in clear-text.
	klog.ClampLevel(6)
	klog.SetLogger(log.With(logger, "component", "k8s_client_runtime"))

	level.Info(logger).Log("msg", "Starting Prometheus", "version", version.Info())
	level.Info(logger).Log("build_context", version.BuildContext())
	level.Info(logger).Log("host_details", prom_runtime.Uname())
	level.Info(logger).Log("fd_limits", prom_runtime.FdLimits())
	level.Info(logger).Log("vm_limits", prom_runtime.VmLimits())

	var (
		localStorage  = &tsdb.ReadyStorage{}
		remoteStorage = remote.NewStorage(log.With(logger, "component", "remote"), prometheus.DefaultRegisterer, localStorage.StartTime, cfg.localStoragePath, time.Duration(cfg.RemoteFlushDeadline))
		fanoutStorage = storage.NewFanout(logger, localStorage, remoteStorage)
	)

	var (
		ctxWeb, cancelWeb = context.WithCancel(context.Background())

		opts = promql.EngineOpts{
			Logger:             log.With(logger, "component", "query engine"),
			Reg:                prometheus.DefaultRegisterer,
			MaxConcurrent:      cfg.queryConcurrency,
			MaxSamples:         cfg.queryMaxSamples,
			Timeout:            time.Duration(cfg.queryTimeout),
			ActiveQueryTracker: promql.NewActiveQueryTracker(cfg.localStoragePath, cfg.queryConcurrency, log.With(logger, "component", "activeQueryTracker")),
		}

		queryEngine = promql.NewEngine(opts)
	)

	cfg.web.Context = ctxWeb
	cfg.web.TSDB = localStorage.Get
	cfg.web.Storage = fanoutStorage
	cfg.web.QueryEngine = queryEngine
	cfg.web.TSDBCfg = cfg.tsdb

	cfg.web.Version = &web.PrometheusVersion{
		Version:   version.Version,
		Revision:  version.Revision,
		Branch:    version.Branch,
		BuildUser: version.BuildUser,
		BuildDate: version.BuildDate,
		GoVersion: version.GoVersion,
	}

	cfg.web.Flags = map[string]string{}

	// Exclude kingpin default flags to expose only Prometheus ones.
	boilerplateFlags := kingpin.New("", "").Version("")
	for _, f := range a.Model().Flags {
		if boilerplateFlags.GetFlag(f.Name) != nil {
			continue
		}

		cfg.web.Flags[f.Name] = f.Value.String()
	}

	// Depends on cfg.web.ScrapeManager so needs to be after cfg.web.ScrapeManager = scrapeManager.
	webHandler := web.New(log.With(logger, "component", "web"), &cfg.web)

	// Monitor outgoing connections on default transport with conntrack.
	http.DefaultTransport.(*http.Transport).DialContext = conntrack.NewDialContextFunc(
		conntrack.DialWithTracing(),
	)

	reloaders := []func(cfg *config.Config) error{
		remoteStorage.ApplyConfig,
		webHandler.ApplyConfig,
	}

	prometheus.MustRegister(configSuccess)
	prometheus.MustRegister(configSuccessTime)

	// Start all components while we wait for TSDB to open but only load
	// initial config and mark ourselves as ready after it completed.
	dbOpen := make(chan struct{})

	// sync.Once is used to make sure we can close the channel at different execution stages(SIGTERM or when the config is loaded).
	type closeOnce struct {
		C     chan struct{}
		once  sync.Once
		Close func()
	}
	// Wait until the server is ready to handle reloading.
	reloadReady := &closeOnce{
		C: make(chan struct{}),
	}
	reloadReady.Close = func() {
		reloadReady.once.Do(func() {
			close(reloadReady.C)
		})
	}

	var g run.Group
	{
		// Termination handler.
		term := make(chan os.Signal, 1)
		signal.Notify(term, os.Interrupt, syscall.SIGTERM)
		cancel := make(chan struct{})
		g.Add(
			func() error {
				// Don't forget to release the reloadReady channel so that waiting blocks can exit normally.
				select {
				case <-term:
					level.Warn(logger).Log("msg", "Received SIGTERM, exiting gracefully...")
					reloadReady.Close()
				case <-webHandler.Quit():
					level.Warn(logger).Log("msg", "Received termination request via web service, exiting gracefully...")
				case <-cancel:
					reloadReady.Close()
				}
				return nil
			},
			func(err error) {
				close(cancel)
			},
		)
	}
	{
		// Reload handler.

		// Make sure that sighup handler is registered with a redirect to the channel before the potentially
		// long and synchronous tsdb init.
		hup := make(chan os.Signal, 1)
		signal.Notify(hup, syscall.SIGHUP)
		cancel := make(chan struct{})
		g.Add(
			func() error {
				<-reloadReady.C

				for {
					select {
					case <-hup:
						if err := reloadConfig(cfg.configFile, logger, reloaders...); err != nil {
							level.Error(logger).Log("msg", "Error reloading config", "err", err)
						}
					case rc := <-webHandler.Reload():
						if err := reloadConfig(cfg.configFile, logger, reloaders...); err != nil {
							level.Error(logger).Log("msg", "Error reloading config", "err", err)
							rc <- err
						} else {
							rc <- nil
						}
					case <-cancel:
						return nil
					}
				}

			},
			func(err error) {
				// Wait for any in-progress reloads to complete to avoid
				// reloading things after they have been shutdown.
				cancel <- struct{}{}
			},
		)
	}
	{
		// Initial configuration loading.
		cancel := make(chan struct{})
		g.Add(
			func() error {
				select {
				case <-dbOpen:
					break
				// In case a shutdown is initiated before the dbOpen is released
				case <-cancel:
					reloadReady.Close()
					return nil
				}

				if err := reloadConfig(cfg.configFile, logger, reloaders...); err != nil {
					return errors.Wrapf(err, "error loading config from %q", cfg.configFile)
				}

				reloadReady.Close()

				webHandler.Ready()
				level.Info(logger).Log("msg", "Server is ready to receive web requests.")
				<-cancel
				return nil
			},
			func(err error) {
				close(cancel)
			},
		)
	}
	{
		// Rule manager.
	}
	{
		// TSDB.
		cancel := make(chan struct{})
		g.Add(
			func() error {
				level.Info(logger).Log("msg", "Starting TSDB ...")
				if cfg.tsdb.WALSegmentSize != 0 {
					if cfg.tsdb.WALSegmentSize < 10*1024*1024 || cfg.tsdb.WALSegmentSize > 256*1024*1024 {
						return errors.New("flag 'storage.tsdb.wal-segment-size' must be set between 10MB and 256MB")
					}
				}
				db, err := tsdb.Open(
					cfg.localStoragePath,
					log.With(logger, "component", "tsdb"),
					prometheus.DefaultRegisterer,
					&cfg.tsdb,
				)
				if err != nil {
					return errors.Wrapf(err, "opening storage failed")
				}
				level.Info(logger).Log("fs_type", prom_runtime.Statfs(cfg.localStoragePath))
				level.Info(logger).Log("msg", "TSDB started")
				level.Debug(logger).Log("msg", "TSDB options",
					"MinBlockDuration", cfg.tsdb.MinBlockDuration,
					"MaxBlockDuration", cfg.tsdb.MaxBlockDuration,
					"MaxBytes", cfg.tsdb.MaxBytes,
					"NoLockfile", cfg.tsdb.NoLockfile,
					"RetentionDuration", cfg.tsdb.RetentionDuration,
					"WALSegmentSize", cfg.tsdb.WALSegmentSize,
					"AllowOverlappingBlocks", cfg.tsdb.AllowOverlappingBlocks,
					"WALCompression", cfg.tsdb.WALCompression,
				)

				startTimeMargin := int64(2 * time.Duration(cfg.tsdb.MinBlockDuration).Seconds() * 1000)
				localStorage.Set(db, startTimeMargin)
				close(dbOpen)
				<-cancel
				return nil
			},
			func(err error) {
				if err := fanoutStorage.Close(); err != nil {
					level.Error(logger).Log("msg", "Error stopping storage", "err", err)
				}
				close(cancel)
			},
		)
	}
	{
		// Web handler.
		g.Add(
			func() error {
				if err := webHandler.Run(ctxWeb); err != nil {
					return errors.Wrapf(err, "error starting web server")
				}
				return nil
			},
			func(err error) {
				cancelWeb()
			},
		)
	}
	{
		// Notifier.
	}
	if err := g.Run(); err != nil {
		level.Error(logger).Log("err", err)
		os.Exit(1)
	}
	level.Info(logger).Log("msg", "See you next time!")
}

func reloadConfig(filename string, logger log.Logger, rls ...func(*config.Config) error) (err error) {
	level.Info(logger).Log("msg", "Loading configuration file", "filename", filename)

	defer func() {
		if err == nil {
			configSuccess.Set(1)
			configSuccessTime.SetToCurrentTime()
		} else {
			configSuccess.Set(0)
		}
	}()

	conf, err := config.LoadFile(filename)
	if err != nil {
		return errors.Wrapf(err, "couldn't load configuration (--config.file=%q)", filename)
	}

	failed := false
	for _, rl := range rls {
		if err := rl(conf); err != nil {
			level.Error(logger).Log("msg", "Failed to apply configuration", "err", err)
			failed = true
		}
	}
	if failed {
		return errors.Errorf("one or more errors occurred while applying the new configuration (--config.file=%q)", filename)
	}

	promql.SetDefaultEvaluationInterval(time.Duration(conf.GlobalConfig.EvaluationInterval))
	level.Info(logger).Log("msg", "Completed loading of configuration file", "filename", filename)
	return nil
}

func startsOrEndsWithQuote(s string) bool {
	return strings.HasPrefix(s, "\"") || strings.HasPrefix(s, "'") ||
		strings.HasSuffix(s, "\"") || strings.HasSuffix(s, "'")
}

// compileCORSRegexString compiles given string and adds anchors
func compileCORSRegexString(s string) (*regexp.Regexp, error) {
	r, err := relabel.NewRegexp(s)
	if err != nil {
		return nil, err
	}
	return r.Regexp, nil
}

// computeExternalURL computes a sanitized external URL from a raw input. It infers unset
// URL parts from the OS and the given listen address.
func computeExternalURL(u, listenAddr string) (*url.URL, error) {
	if u == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return nil, err
		}
		_, port, err := net.SplitHostPort(listenAddr)
		if err != nil {
			return nil, err
		}
		u = fmt.Sprintf("http://%s:%s/", hostname, port)
	}

	if startsOrEndsWithQuote(u) {
		return nil, errors.New("URL must not begin or end with quotes")
	}

	eu, err := url.Parse(u)
	if err != nil {
		return nil, err
	}

	ppref := strings.TrimRight(eu.Path, "/")
	if ppref != "" && !strings.HasPrefix(ppref, "/") {
		ppref = "/" + ppref
	}
	eu.Path = ppref

	return eu, nil
}
