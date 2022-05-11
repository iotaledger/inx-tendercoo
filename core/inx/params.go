package inx

import (
	flag "github.com/spf13/pflag"
	
	"github.com/iotaledger/hive.go/app"
)

const (
	// CfgINXAddress the INX address to which to connect to.
	CfgINXAddress = "inx.address"
)

var params = &app.ComponentParams{
	Params: func(fs *flag.FlagSet) {
		fs.String(CfgINXAddress, "localhost:9029", "the INX address to which to connect to")
	},
	Masked: nil,
}
