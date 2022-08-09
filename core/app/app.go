package app

import (
	cryptorand "crypto/rand"
	"encoding/binary"
	mathrand "math/rand"

	"github.com/iotaledger/hive.go/core/app"
	"github.com/iotaledger/hive.go/core/app/core/shutdown"
	"github.com/iotaledger/hive.go/core/app/plugins/profiling"
	"github.com/iotaledger/inx-app/inx"
	"github.com/iotaledger/inx-tendercoo/core/decoo"
)

var (
	// Name of the app.
	Name = "inx-tendercoo"

	// Version of the app.
	Version = "0.0.2"
)

func init() {
	// seed math/rand with something cryptographically secure
	var b [8]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		panic("cannot seed math/rand package with cryptographically secure random number generator")
	}
	mathrand.Seed(int64(binary.LittleEndian.Uint64(b[:])))
}

var initComponent = &app.InitComponent{
	Component: &app.Component{
		Name: "App",
	},
	NonHiddenFlags: []string{
		"config",
		"help",
		"version",
		decoo.CfgCoordinatorBootstrap,
		decoo.CfgCoordinatorStartIndex,
		decoo.CfgCoordinatorStartMilestoneID,
		decoo.CfgCoordinatorStartMilestoneBlockID,
	},
}

func App() *app.App {
	return app.New(Name, Version,
		app.WithInitComponent(initComponent),
		app.WithCoreComponents([]*app.CoreComponent{
			inx.CoreComponent,
			decoo.CoreComponent,
			shutdown.CoreComponent,
		}...),
		app.WithPlugins([]*app.Plugin{
			profiling.Plugin,
		}...),
	)
}
