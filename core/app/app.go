package app

import (
	"github.com/gohornet/hornet/core/gracefulshutdown"
	"github.com/gohornet/hornet/plugins/profiling"
	"github.com/iotaledger/hive.go/app"
	"github.com/iotaledger/inx-tendercoo/core/decoo"
	"github.com/iotaledger/inx-tendercoo/core/inx"
)

var (
	// Name of the app.
	Name = "inx-tendercoo"

	// Version of the app.
	Version = "0.0.1"
)

func App() *app.App {
	return app.New(Name, Version,
		app.WithInitComponent(InitComponent),
		app.WithCoreComponents([]*app.CoreComponent{
			inx.CoreComponent,
			decoo.CoreComponent,
			gracefulshutdown.CoreComponent,
		}...),
		app.WithPlugins([]*app.Plugin{
			profiling.Plugin,
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
