package main

import (
	"github.com/gohornet/hornet/core/gracefulshutdown"
	"github.com/gohornet/hornet/pkg/node"
	"github.com/gohornet/inx-coordinator/core/app"
	"github.com/gohornet/inx-coordinator/core/coordinator"
	"github.com/gohornet/inx-coordinator/core/inx"
	"github.com/gohornet/inx-coordinator/plugins/migrator"
	"github.com/gohornet/inx-coordinator/plugins/profiling"
)

func main() {
	node.Run(
		node.WithInitPlugin(app.InitPlugin),
		node.WithCorePlugins([]*node.CorePlugin{
			inx.CorePlugin,
			coordinator.CorePlugin,
			gracefulshutdown.CorePlugin,
		}...),
		node.WithPlugins([]*node.Plugin{
			migrator.Plugin,
			profiling.Plugin,
			//prometheus.Plugin,
		}...),
	)
}
