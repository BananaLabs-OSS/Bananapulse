package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/BananaLabs-OSS/Pulp/run"
	_ "modernc.org/sqlite"
)

const (
	defaultBridgeAddress        = "127.0.0.1:8788"
	bridgeAddressEnv            = "PULP_BRIDGE_ADDR"
	bridgeTokenEnv              = "PULP_BRIDGE_TOKEN"
	bridgeAppManifestEnv        = "PULP_APP_MANIFEST"
	bridgeStorageRootEnv        = "PULP_STORAGE_ROOT"
	bridgeMonitorAdminEnv       = "PULP_BRIDGE_ENABLE_MONITOR_ADMIN"
	bridgeMonitorIngestEnv      = "PULP_BRIDGE_ENABLE_MONITOR_INGEST"
	bridgeMonitorSweepEnv       = "PULP_BRIDGE_ENABLE_MONITOR_SWEEP"
	bridgeSubscriberAdminEnv    = "PULP_BRIDGE_ENABLE_SUBSCRIBER_ADMIN"
	bridgeMigrationEnv          = "PULP_BRIDGE_ENABLE_MIGRATION"
	bridgeAuthEnv               = "PULP_BRIDGE_ENABLE_AUTH"
	bridgeAuthAdminEnv          = "PULP_BRIDGE_ENABLE_AUTH_ADMIN"
	bridgeSourceAdminEnv        = "PULP_BRIDGE_ENABLE_SOURCE_ADMIN"
	bridgeUnsubscribeBaseURLEnv = "PULP_SUBSCRIBER_UNSUBSCRIBE_BASE_URL"
)

type bridgeConfig struct {
	Address            string
	Token              string
	AppManifest        string
	StorageRoot        string
	Families           bridgeFamilies
	UnsubscribeBaseURL string
}

func bridgeConfigFromEnv() bridgeConfig {
	config := bridgeConfig{
		Address:            os.Getenv(bridgeAddressEnv),
		Token:              os.Getenv(bridgeTokenEnv),
		AppManifest:        os.Getenv(bridgeAppManifestEnv),
		StorageRoot:        os.Getenv(bridgeStorageRootEnv),
		UnsubscribeBaseURL: os.Getenv(bridgeUnsubscribeBaseURLEnv),
		Families: bridgeFamilies{
			monitorAdmin:    bridgeEnvEnabled(bridgeMonitorAdminEnv),
			monitorIngest:   bridgeEnvEnabled(bridgeMonitorIngestEnv),
			monitorSweep:    bridgeEnvEnabled(bridgeMonitorSweepEnv),
			subscriberAdmin: bridgeEnvEnabled(bridgeSubscriberAdminEnv),
			migration:       bridgeEnvEnabled(bridgeMigrationEnv),
			auth:            bridgeEnvEnabled(bridgeAuthEnv),
			authAdmin:       bridgeEnvEnabled(bridgeAuthAdminEnv),
			sourceAdmin:     bridgeEnvEnabled(bridgeSourceAdminEnv),
		},
	}
	if config.Address == "" {
		config.Address = defaultBridgeAddress
	}
	if config.AppManifest == "" {
		config.AppManifest = filepath.Join("..", "application", "pulp.app.toml")
	}
	if config.StorageRoot == "" {
		config.StorageRoot = "./data"
	}
	return config
}

func bridgeEnvEnabled(name string) bool {
	enabled, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(name)))
	return err == nil && enabled
}

type bridgeProviderObserver struct {
	ready chan run.ApplicationProviderAccess
}

func (o *bridgeProviderObserver) AfterApplicationStart(context.Context, run.ApplicationIdentity) error {
	return nil
}

func (o *bridgeProviderObserver) AfterApplicationStartWithProvider(
	_ context.Context,
	_ run.ApplicationIdentity,
	access run.ApplicationProviderAccess,
) error {
	select {
	case o.ready <- access:
		return nil
	default:
		return errors.New("application provider access already supplied")
	}
}

func (o *bridgeProviderObserver) BeforeApplicationShutdown(context.Context, run.ApplicationIdentity) error {
	return nil
}

type runningBridge struct {
	runtime      run.ApplicationRuntime
	server       *http.Server
	listener     net.Listener
	outboxWorker *outboxService
	sourceSagaDB *sql.DB
	serveErr     chan error
}

func startBridge(ctx context.Context, config bridgeConfig) (*runningBridge, error) {
	if config.Address == "" || config.AppManifest == "" || config.StorageRoot == "" {
		return nil, errors.New("bridge address, application manifest, and storage root are required")
	}
	if config.Token == "" && !bridgeAddressIsLoopback(config.Address) {
		return nil, errors.New("a bridge token is required when listening beyond loopback")
	}
	if config.Families.sourceAdmin &&
		(!config.Families.monitorAdmin || !config.Families.auth || !config.Families.authAdmin) {
		return nil, errors.New("source admin requires monitor admin, auth, and auth admin families")
	}
	observer := &bridgeProviderObserver{ready: make(chan run.ApplicationProviderAccess, 1)}
	runtime, err := run.NewDirectApplicationRuntime(config.AppManifest, run.DirectApplicationOptions{
		StorageRoot: config.StorageRoot,
		Lifecycle:   observer,
	})
	if err != nil {
		return nil, fmt.Errorf("create Bananapulse application runtime: %w", err)
	}
	if err := runtime.Start(ctx); err != nil {
		return nil, fmt.Errorf("start Bananapulse application runtime: %w", err)
	}
	shutdownRuntime := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = runtime.Shutdown(shutdownCtx)
	}

	var access run.ApplicationProviderAccess
	select {
	case access = <-observer.ready:
	case <-ctx.Done():
		shutdownRuntime()
		return nil, fmt.Errorf("wait for Bananapulse provider access: %w", ctx.Err())
	}
	client, err := newApplicationClient(access)
	if err != nil {
		shutdownRuntime()
		return nil, err
	}
	if _, err := runLegacyImportAtStartup(
		ctx,
		legacyImportStartupConfigFromEnv(),
		config.StorageRoot,
		client,
	); err != nil {
		shutdownRuntime()
		return nil, fmt.Errorf("run fenced legacy import: %w", err)
	}
	notificationsEnabled := config.Families.monitorAdmin || config.Families.monitorIngest || config.Families.monitorSweep
	if notificationsEnabled && config.UnsubscribeBaseURL == "" {
		shutdownRuntime()
		return nil, errors.New("subscriber unsubscribe base URL is required when monitor notification families are enabled")
	}
	if config.UnsubscribeBaseURL != "" {
		if err := configureSubscriberDelivery(ctx, client, config.UnsubscribeBaseURL); err != nil {
			shutdownRuntime()
			return nil, fmt.Errorf("configure subscriber delivery: %w", err)
		}
	}
	bridge, err := newHTTPBridgeWithFamilies(client, config.Token, config.Families)
	if err != nil {
		shutdownRuntime()
		return nil, err
	}
	var sourceSagaDB *sql.DB
	if config.Families.sourceAdmin {
		sourceSagaDB, err = sql.Open("sqlite", filepath.Join(config.StorageRoot, "pulp-host-source-sagas.sqlite"))
		if err != nil {
			shutdownRuntime()
			return nil, fmt.Errorf("open source saga checkpoints: %w", err)
		}
		sourceSagaDB.SetMaxOpenConns(1)
		sourceSagaDB.SetMaxIdleConns(1)
		store, err := newSQLiteSourceSagaCheckpointStore(sourceSagaDB)
		if err != nil {
			_ = sourceSagaDB.Close()
			shutdownRuntime()
			return nil, err
		}
		bridge.sources, err = newSourceLifecycleService(client, store)
		if err != nil {
			_ = sourceSagaDB.Close()
			shutdownRuntime()
			return nil, err
		}
	}
	var outboxWorker *outboxService
	if resend, enabled := resendConfigFromEnv(); enabled {
		sender, err := newResendSender(resend)
		if err != nil {
			closeBridgeDB(sourceSagaDB)
			shutdownRuntime()
			return nil, fmt.Errorf("configure Bananapulse email delivery: %w", err)
		}
		outboxWorker, err = startOutboxService(client, sender, outboxServiceConfigFromEnv())
		if err != nil {
			closeBridgeDB(sourceSagaDB)
			shutdownRuntime()
			return nil, fmt.Errorf("start Bananapulse email outbox worker: %w", err)
		}
	}
	listener, err := net.Listen("tcp", config.Address)
	if err != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = outboxWorker.shutdown(shutdownCtx)
		closeBridgeDB(sourceSagaDB)
		shutdownRuntime()
		return nil, fmt.Errorf("listen on Pulp bridge address: %w", err)
	}
	server := &http.Server{
		Handler:           bridge.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	running := &runningBridge{
		runtime: runtime, server: server, listener: listener, outboxWorker: outboxWorker,
		sourceSagaDB: sourceSagaDB,
		serveErr:     make(chan error, 1),
	}
	go func() {
		err := server.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			running.serveErr <- err
		}
		close(running.serveErr)
	}()
	return running, nil
}

func closeBridgeDB(db *sql.DB) {
	if db != nil {
		_ = db.Close()
	}
}

func bridgeAddressIsLoopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (b *runningBridge) shutdown(ctx context.Context) error {
	if b == nil {
		return nil
	}
	var result error
	if b.server != nil {
		if err := b.server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			result = err
		}
	}
	if err := b.outboxWorker.shutdown(ctx); err != nil && result == nil {
		result = err
	}
	if b.sourceSagaDB != nil {
		if err := b.sourceSagaDB.Close(); err != nil && result == nil {
			result = err
		}
	}
	if b.runtime != nil {
		if err := b.runtime.Shutdown(ctx); err != nil && result == nil {
			result = err
		}
	}
	return result
}

func runBridgeMain() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	bridge, err := startBridge(ctx, bridgeConfigFromEnv())
	if err != nil {
		return err
	}
	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-bridge.serveErr:
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := bridge.shutdown(shutdownCtx); err != nil {
		return err
	}
	if serveErr != nil {
		return fmt.Errorf("serve Pulp bridge: %w", serveErr)
	}
	return nil
}
