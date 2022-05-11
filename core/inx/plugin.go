package inx

import (
	"context"

	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/retry"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"go.uber.org/dig"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/gohornet/hornet/pkg/shutdown"
	"github.com/gohornet/inx-coordinator/pkg/daemon"
	"github.com/gohornet/inx-coordinator/pkg/nodebridge"
	"github.com/gohornet/inx-coordinator/plugins/migrator"
	"github.com/iotaledger/hive.go/app"
	"github.com/iotaledger/hive.go/configuration"
	inx "github.com/iotaledger/inx/go"
)

func init() {
	CoreComponent = &app.CoreComponent{
		Component: &app.Component{
			Name:     "INX",
			DepsFunc: func(cDeps dependencies) { deps = cDeps },
			Params:   params,
			Provide:  provide,
			Run:      run,
		},
	}
}

type dependencies struct {
	dig.In
	AppConfig  *configuration.Configuration `name:"appConfig"`
	NodeBridge *nodebridge.NodeBridge
	Connection *grpc.ClientConn
}

var (
	CoreComponent *app.CoreComponent
	deps          dependencies
)

func provide(c *dig.Container) error {

	type inxDeps struct {
		dig.In
		AppConfig       *configuration.Configuration `name:"appConfig"`
		ShutdownHandler *shutdown.ShutdownHandler
	}

	type inxDepsOut struct {
		dig.Out
		Connection *grpc.ClientConn
		INXClient  inx.INXClient
	}

	if err := c.Provide(func(deps inxDeps) (inxDepsOut, error) {
		conn, err := grpc.Dial(deps.AppConfig.String(CfgINXAddress),
			grpc.WithChainUnaryInterceptor(grpc_retry.UnaryClientInterceptor(), grpc_prometheus.UnaryClientInterceptor),
			grpc.WithStreamInterceptor(grpc_prometheus.StreamClientInterceptor),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			return inxDepsOut{}, err
		}
		client := inx.NewINXClient(conn)

		return inxDepsOut{
			Connection: conn,
			INXClient:  client,
		}, nil
	}); err != nil {
		return err
	}

	if err := c.Provide(func(client inx.INXClient) (*nodebridge.NodeBridge, error) {
		migrationsEnabled := !CoreComponent.App.IsPluginSkipped(migrator.Plugin)
		return nodebridge.NewNodeBridge(CoreComponent.Daemon().ContextStopped(),
			client,
			migrationsEnabled,
			CoreComponent.Logger())
	}); err != nil {
		return err
	}

	return nil
}

func run() error {
	return CoreComponent.Daemon().BackgroundWorker("INX", func(ctx context.Context) {
		CoreComponent.LogInfo("Starting NodeBridge")
		deps.NodeBridge.Run(ctx)
		CoreComponent.LogInfo("Stopped NodeBridge")
		deps.Connection.Close()
	}, daemon.PriorityDisconnectINX)
}
