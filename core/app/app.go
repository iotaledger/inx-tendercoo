package app

import (
	"github.com/gohornet/hornet/core/gracefulshutdown"
	"github.com/gohornet/hornet/plugins/profiling"
	"github.com/gohornet/inx-coordinator/core/coordinator"
	"github.com/gohornet/inx-coordinator/core/inx"
	"github.com/gohornet/inx-coordinator/plugins/migrator"
	"github.com/iotaledger/hive.go/app"
)

var (
	// Name of the app.
	Name = "inx-coordinator"

	// Version of the app.
	Version = "0.3.1"
)

func App() *app.App {
	return app.New(Name, Version,
		app.WithInitComponent(InitComponent),
		app.WithCoreComponents([]*app.CoreComponent{
			inx.CoreComponent,
			coordinator.CoreComponent,
			gracefulshutdown.CoreComponent,
		}...),
		app.WithPlugins([]*app.Plugin{
			migrator.Plugin,
			profiling.Plugin,
			//prometheus.Plugin,
		}...),
	)
}

var (
	InitComponent *app.InitComponent
)

func init() {
	InitComponent = &app.InitComponent{
		Component: &app.Component{
			Name: "App",
		},
		NonHiddenFlags: []string{
			"config",
			"help",
			"version",
			"migratorBootstrap",
			"migratorStartIndex",
			"cooBootstrap",
			"cooStartIndex",
		},
	}
}
