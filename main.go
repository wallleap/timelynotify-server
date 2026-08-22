package main

import (
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"syscall"
	"time"

	"github.com/wallleap/timelynotify-server/apns"
	"github.com/wallleap/timelynotify-server/database"
	"github.com/wallleap/timelynotify-server/internal/gotifycompat"
	"github.com/wallleap/timelynotify-server/internal/logging"
	"github.com/wallleap/timelynotify-server/internal/metrics"

	jsoniter "github.com/json-iterator/go"

	"github.com/gofiber/fiber/v2"

	"github.com/mritd/logger"
	"github.com/urfave/cli/v2"
)

var (
	version   string
	buildDate string
	commitID  string
)

var db database.Database

// tnMetrics is the global Prometheus registry. Registered as nil until
// runServer initializes it, so package-level routes can safely reference it.
var tnMetrics *metrics.Registry

func main() {
	app := &cli.App{
		Name:    "timelynotify-server",
		Usage:   "Push Server For TimelyNotify (fork of Bark Server)",
		Version: fmt.Sprintf("%s %s %s", version, commitID, buildDate),
		Flags:   getAppFlags(),
		Authors: []*cli.Author{
			{Name: "mritd", Email: "mritd@linux.com"},
			{Name: "Finb", Email: "to@day.app"},
			{Name: "wallleap"},
		},
		Action: runServer,
	}

	if err := app.Run(os.Args); err != nil {
		logger.Error(err)
		os.Exit(1)
	}
}
func runServer(c *cli.Context) error {
	if err := applyLogConfig(c); err != nil {
		return err
	}
	tnMetrics = metrics.New()
	network := determineNetwork(c)
	fiberApp := createFiberApp(c, network)
	setupRouter(c, fiberApp)
	initializeDatabase(c)
	initGotifyCompat(c)
	setupRateLimits(c.Int("rate-limit-ip"), c.Int("rate-limit-burst"), c.Bool("rate-limit-push"))
	setupGracefulShutdown(fiberApp)
	return startServer(c, fiberApp, network)
}

// applyLogConfig applies --log-level and --log-format to the mritd logger.
// Invalid values are reported as startup errors rather than being silently
// ignored.
func applyLogConfig(c *cli.Context) error {
	lvl, err := logging.ParseLevel(c.String("log-level"))
	if err != nil {
		return err
	}
	fmtLevel, err := logging.ParseFormat(c.String("log-format"))
	if err != nil {
		return err
	}
	logging.ApplyLevel(lvl)
	logging.ApplyFormat(fmtLevel)
	return nil
}

// initGotifyCompat initializes the gotify-compatible monitoring interface
// (WebSocket /stream + /message + /version) used by hotify-bridge. Persistence
// falls back to in-memory when the data directory is unusable; the returned
// error is logged, never fatal to the server.
func initGotifyCompat(c *cli.Context) {
	svc, err := gotifycompat.Init(gotifycompat.Config{
		DataDir:     c.String("data"),
		ClientToken: c.String("gotify-client-token"),
		Version:     version,
		MaxMessages: c.Int("gotify-max-messages"),
	})
	if err != nil {
		logger.Errorf("gotify compat init failed: %v", err)
		return
	}
	gotifyService = svc

	switch svc.TokenSource() {
	case gotifycompat.TokenSourceOperator:
		logger.Info("Gotify-compatible stream ready. Using operator-supplied client token.")
	case gotifycompat.TokenSourceGenerated:
		logger.Infof("Gotify-compatible stream ready. Generated client token (set bridge gotify_token to this): %s", svc.ClientToken())
	case gotifycompat.TokenSourcePersisted:
		logger.Info("Gotify-compatible stream ready. Client token persisted in <data>/gotify.db; pin it via BARK_SERVER_GOTIFY_CLIENT_TOKEN, or delete that file to regenerate.")
	default:
		logger.Info("Gotify-compatible stream ready.")
	}
}

// determineNetwork checks if unix socket is configured
func determineNetwork(c *cli.Context) string {
	if c.String("unix-socket") != "" {
		return "unix"
	}
	return "tcp"
}

func createFiberApp(c *cli.Context, network string) *fiber.App {
	return fiber.New(fiber.Config{
		ServerHeader:      "TimelyNotify",
		CaseSensitive:     c.Bool("case-sensitive"),
		StrictRouting:     c.Bool("strict-routing"),
		Concurrency:       c.Int("concurrency"),
		ReadTimeout:       c.Duration("read-timeout"),
		WriteTimeout:      c.Duration("write-timeout"),
		IdleTimeout:       c.Duration("idle-timeout"),
		ProxyHeader:       c.String("proxy-header"),
		ReduceMemoryUsage: c.Bool("reduce-memory-usage"),
		JSONEncoder:       jsoniter.Marshal,
		Network:           network,
		ErrorHandler:      customErrorHandler,
	})
}

func customErrorHandler(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	if e, ok := err.(*fiber.Error); ok {
		code = e.Code
	}
	return c.Status(code).JSON(CommonResp{
		Code:      code,
		Message:   err.Error(),
		Timestamp: time.Now().Unix(),
	})
}

// authentication and routes
func setupRouter(c *cli.Context, fiberApp *fiber.App) {
	fiberRouter := fiberApp.Group(c.String("url-prefix"))
	// Mount the access-log/recover/metrics middleware before Basic Auth so
	// that auth-rejected requests (which return before Next) are still logged
	// and instrumented.
	routerSetupCommon(fiberRouter)
	routerAuth(c.String("user"), c.String("password"), fiberRouter, c.String("url-prefix"))
	routerSetupRoutes(fiberRouter)
}

func initializeDatabase(c *cli.Context) {
	if c.Bool("serverless") {
		db = database.NewEnvBase()
		return
	}

	if dsn := c.String("dsn"); dsn != "" {
		if c.Bool("mysql-tls") {
			db = database.NewMySQLWithTLS(
				dsn,
				c.String("mysql-tls-name"),
				c.String("mysql-ca"),
				c.String("mysql-client-cert"),
				c.String("mysql-client-key"),
				c.Bool("mysql-tls-skip-verify"),
			)
		} else {
			db = database.NewMySQL(dsn)
		}
		return
	}

	db = database.NewBboltdb(c.String("data"))
}

func setupGracefulShutdown(fiberApp *fiber.App) {
	go func() {
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
		for range sigs {
			logger.Warn("Received a termination signal, timelynotify-server shutdown...")
			if err := fiberApp.Shutdown(); err != nil {
				logger.Errorf("Server forced to shutdown error: %v", err)
			}
			if err := db.Close(); err != nil {
				logger.Errorf("Database close error: %v", err)
			}
		}
	}()
}

func startServer(c *cli.Context, fiberApp *fiber.App, network string) error {
	if network == "tcp" {
		addr := c.String("addr")
		logger.Infof("TimelyNotify Server Listen at: %s , Database: %s", addr, reflect.TypeOf(db))

		cert, key := c.String("cert"), c.String("key")
		if cert != "" && key != "" {
			return fiberApp.ListenTLS(addr, cert, key)
		}
		return fiberApp.Listen(addr)
	}

	// Unix socket
	socket := c.String("unix-socket")
	os.Remove(socket)
	logger.Infof("TimelyNotify Server Listen at: %s , Database: %s", socket, reflect.TypeOf(db))
	return fiberApp.Listen(socket)
}

func getAppFlags() []cli.Flag {
	return []cli.Flag{
		&cli.StringFlag{
			Name:    "addr",
			Usage:   "Server listen address",
			EnvVars: []string{"BARK_SERVER_ADDRESS"},
			Value:   "0.0.0.0:8080",
		},
		&cli.StringFlag{
			Name:    "unix-socket",
			Usage:   "Server listen unix socket",
			EnvVars: []string{"BARK_SERVER_UNIX_SOCKET"},
			Value:   "",
		},
		&cli.StringFlag{
			Name:    "url-prefix",
			Usage:   "Serve URL Prefix",
			EnvVars: []string{"BARK_SERVER_URL_PREFIX"},
			Value:   "/",
		},
		&cli.StringFlag{
			Name:    "data",
			Usage:   "Server data storage dir",
			EnvVars: []string{"BARK_SERVER_DATA_DIR"},
			Value:   "/data",
		},
		&cli.StringFlag{
			Name:    "dsn",
			Usage:   "MySQL DSN user:pass@tcp(host)/dbname",
			EnvVars: []string{"BARK_SERVER_DSN"},
			Value:   "",
		},
		&cli.BoolFlag{
			Name:    "mysql-tls",
			Usage:   "Enable TLS/SSL for MySQL connections",
			EnvVars: []string{"BARK_SERVER_MYSQL_TLS"},
			Value:   false,
		},
		&cli.BoolFlag{
			Name:    "mysql-tls-skip-verify",
			Usage:   "Skip verification of the MySQL server's TLS/SSL certificate",
			EnvVars: []string{"BARK_SERVER_MYSQL_TLS_SKIP_VERIFY"},
			Value:   false,
		},
		&cli.StringFlag{
			Name:    "mysql-ca",
			Usage:   "MySQL TLS/SSL CA certificate file (PEM): /path/to/ca.pem",
			EnvVars: []string{"BARK_SERVER_MYSQL_CA"},
			Value:   "",
		},
		&cli.StringFlag{
			Name:    "mysql-client-cert",
			Usage:   "MySQL TLS/SSL client cert (PEM): /path/to/client-cert.pem",
			EnvVars: []string{"BARK_SERVER_MYSQL_CLIENT_CERT"},
			Value:   "",
		},
		&cli.StringFlag{
			Name:    "mysql-client-key",
			Usage:   "MySQL TLS/SSL client key (PEM): /path/to/client-key.pem",
			EnvVars: []string{"BARK_SERVER_MYSQL_CLIENT_KEY"},
			Value:   "",
		},
		&cli.StringFlag{
			Name:    "mysql-tls-name",
			Usage:   "Name of the TLS/SSL config to register for MySQL",
			EnvVars: []string{"BARK_SERVER_MYSQL_TLS_NAME"},
			Value:   "custom",
		},
		&cli.BoolFlag{
			Name:    "serverless",
			Usage:   "serverless mode",
			EnvVars: []string{"BARK_SERVER_SERVERLESS"},
			Value:   false,
		},
		&cli.StringFlag{
			Name:    "cert",
			Usage:   "Server TLS certificate",
			EnvVars: []string{"BARK_SERVER_CERT"},
			Value:   "",
		},
		&cli.StringFlag{
			Name:    "key",
			Usage:   "Server TLS certificate key",
			EnvVars: []string{"BARK_SERVER_KEY"},
			Value:   "",
		},
		&cli.BoolFlag{
			Name:    "case-sensitive",
			Usage:   "Enable HTTP URL case sensitive",
			EnvVars: []string{"BARK_SERVER_CASE_SENSITIVE"},
			Value:   false,
		},
		&cli.BoolFlag{
			Name:    "strict-routing",
			Usage:   "Enable strict routing distinction",
			EnvVars: []string{"BARK_SERVER_STRICT_ROUTING"},
			Value:   false,
		},
		&cli.BoolFlag{
			Name:    "reduce-memory-usage",
			Usage:   "Aggressively reduces memory usage at the cost of higher CPU usage if set to true",
			EnvVars: []string{"BARK_SERVER_REDUCE_MEMORY_USAGE"},
			Value:   false,
		},
		&cli.StringFlag{
			Name:    "user",
			Usage:   "Basic auth username",
			EnvVars: []string{"BARK_SERVER_BASIC_AUTH_USER"},
			Value:   "",
		},
		&cli.StringFlag{
			Name:    "password",
			Usage:   "Basic auth password",
			EnvVars: []string{"BARK_SERVER_BASIC_AUTH_PASSWORD"},
			Value:   "",
		},
		&cli.StringFlag{
			Name:    "proxy-header",
			Usage:   "The remote IP address used by the server for proxy header detection",
			EnvVars: []string{"BARK_SERVER_PROXY_HEADER"},
			Value:   "",
		},
		&cli.StringFlag{
			Name:    "gotify-client-token",
			Usage:   "Gotify-compatible client token for hotify-bridge monitoring; auto-generated and persisted if empty",
			EnvVars: []string{"BARK_SERVER_GOTIFY_CLIENT_TOKEN"},
			Value:   "",
		},
		&cli.IntFlag{
			Name:    "gotify-max-messages",
			Usage:   "Maximum number of gotify messages retained in <data>/gotify.db, 0 uses default (1000)",
			EnvVars: []string{"BARK_SERVER_GOTIFY_MAX_MESSAGES"},
			Value:   0,
		},
		&cli.IntFlag{
			Name:    "max-batch-push-count",
			Usage:   "Maximum number of batch pushes allowed, -1 means no limit",
			EnvVars: []string{"BARK_SERVER_MAX_BATCH_PUSH_COUNT"},
			Value:   -1,
			Action:  func(ctx *cli.Context, v int) error { SetMaxBatchPushCount(v); return nil },
		},
		&cli.IntFlag{
			Name:    "max-apns-client-count",
			Usage:   "Maximum number of APNs client connections",
			EnvVars: []string{"BARK_SERVER_MAX_APNS_CLIENT_COUNT"},
			Value:   1,
			Action:  func(ctx *cli.Context, v int) error { return apns.ReCreateAPNS(v) },
		},
		&cli.IntFlag{
			Name:    "rate-limit-ip",
			Usage:   "Per-IP request rate limit (requests per second) on /register and /mcp; 0 disables",
			EnvVars: []string{"BARK_SERVER_RATE_LIMIT_IP"},
			Value:   0,
		},
		&cli.IntFlag{
			Name:    "rate-limit-burst",
			Usage:   "Burst window (tokens) for the IP rate limit; must be >= 1, defaults to rate",
			EnvVars: []string{"BARK_SERVER_RATE_LIMIT_BURST"},
			Value:   0,
		},
		&cli.BoolFlag{
			Name:    "rate-limit-push",
			Usage:   "Also apply the IP rate limit to /push and /:device_key push endpoints (unlimited by default)",
			EnvVars: []string{"BARK_SERVER_RATE_LIMIT_PUSH"},
			Value:   false,
		},
		&cli.IntFlag{
			Name:    "concurrency",
			Usage:   "Maximum number of concurrent connections",
			EnvVars: []string{"BARK_SERVER_CONCURRENCY"},
			Value:   256 * 1024,
			Hidden:  true,
		},
		&cli.DurationFlag{
			Name:    "read-timeout",
			Usage:   "The amount of time allowed to read the full request, including the body",
			EnvVars: []string{"BARK_SERVER_READ_TIMEOUT"},
			Value:   3 * time.Second,
			Hidden:  true,
		},
		&cli.DurationFlag{
			Name:    "write-timeout",
			Usage:   "The maximum duration before timing out writes of the response",
			EnvVars: []string{"BARK_SERVER_WRITE_TIMEOUT"},
			Value:   3 * time.Second,
			Hidden:  true,
		},
		&cli.DurationFlag{
			Name:    "idle-timeout",
			Usage:   "The maximum amount of time to wait for the next request when keep-alive is enabled",
			EnvVars: []string{"BARK_SERVER_IDLE_TIMEOUT"},
			Value:   10 * time.Second,
			Hidden:  true,
		},
		&cli.StringFlag{
			Name:    "log-level",
			Usage:   "Log level: debug|info|warn|error (default info)",
			EnvVars: []string{"BARK_SERVER_LOG_LEVEL"},
			Value:   "",
		},
		&cli.StringFlag{
			Name:    "log-format",
			Usage:   "Log format: console|json (default console)",
			EnvVars: []string{"BARK_SERVER_LOG_FORMAT"},
			Value:   "",
		},
	}
}
