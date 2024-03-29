package decoo

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tmconfig "github.com/tendermint/tendermint/config"
	tmed25519 "github.com/tendermint/tendermint/crypto/ed25519"
	tendermintlog "github.com/tendermint/tendermint/libs/log"
	tmnode "github.com/tendermint/tendermint/node"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/privval"
	"github.com/tendermint/tendermint/proxy"
	tmtypes "github.com/tendermint/tendermint/types"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/iotaledger/hive.go/logger"
	"github.com/iotaledger/inx-tendercoo/pkg/decoo/types"
)

// TenderLogger is a simple wrapper for the Tendermint logger.
type TenderLogger struct {
	*logger.Logger
	level zap.AtomicLevel
}

// Debug logs a message with some additional context. The variadic key-value pairs are treated as they are in With.
func (l TenderLogger) Debug(msg string, keyVals ...interface{}) {
	if l.level.Enabled(zap.DebugLevel) {
		l.Logger.Debugw(msg, keyVals...)
	}
}

// Info logs a message with some additional context. The variadic key-value pairs are treated as they are in With.
func (l TenderLogger) Info(msg string, keyVals ...interface{}) {
	if l.level.Enabled(zap.InfoLevel) {
		l.Logger.Infow(msg, keyVals...)
	}
}

// Error logs a message with some additional context. The variadic key-value pairs are treated as they are in With.
func (l TenderLogger) Error(msg string, keyVals ...interface{}) {
	if l.level.Enabled(zap.ErrorLevel) {
		l.Logger.Errorw(msg, keyVals...)
	}
}

// With adds a variadic number of key-value pairs to the logging context.
func (l TenderLogger) With(keyVals ...interface{}) tendermintlog.Logger {
	return TenderLogger{l.Logger.With(keyVals...), l.level}
}

// NewTenderLogger creates a new Tendermint compatible logger.
func NewTenderLogger(log *logger.Logger, l zapcore.Level) TenderLogger {
	return TenderLogger{log.Desugar().WithOptions(zap.AddCallerSkip(1)).Sugar(), zap.NewAtomicLevelAt(l)}
}

func initTendermintConfig() (*tmconfig.Config, error) {
	consensusPrivateKey, err := privateKeyFromStringOrFile(Parameters.Tendermint.ConsensusPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to load consensus private key: %w", err)
	}
	tmConsensusKey := tmed25519.PrivKey(consensusPrivateKey)

	rootDir := Parameters.Tendermint.Root
	tmconfig.EnsureRoot(rootDir)
	conf := tmconfig.DefaultConfig().SetRoot(rootDir)
	conf.P2P.ListenAddress = "tcp://" + Parameters.Tendermint.BindAddress
	conf.P2P.PexReactor = false

	// consensus parameters
	conf.Consensus.TimeoutCommit = Parameters.Tendermint.Consensus.BlockInterval
	conf.Consensus.SkipTimeoutCommit = Parameters.Tendermint.Consensus.SkipBlockTimeout
	conf.Consensus.CreateEmptyBlocks = Parameters.Tendermint.Consensus.CreateEmptyBlocks
	conf.Consensus.CreateEmptyBlocksInterval = Parameters.Tendermint.Consensus.CreateEmptyBlocksInterval

	// mempool parameters
	// limit the mempool to at most 1000 transactions of up to 1 KB
	conf.Mempool.Size = 1000
	conf.Mempool.MaxTxBytes = 1024
	conf.Mempool.MaxTxsBytes = int64(conf.Mempool.Size) * int64(conf.Mempool.MaxTxBytes)

	// private validator
	privValKeyFile := conf.PrivValidatorKeyFile()
	privValStateFile := conf.PrivValidatorStateFile()
	if fileExists(privValKeyFile) && fileExists(privValStateFile) {
		_ = privval.LoadFilePV(privValKeyFile, privValStateFile)
		log.Infow("Found private validator", "keyFile", privValKeyFile,
			"stateFile", privValStateFile)
	} else {
		pv := privval.NewFilePV(tmConsensusKey, privValKeyFile, privValStateFile)
		pv.Save()

		log.Infow("Generated private validator", "keyFile", privValKeyFile, "stateFile", privValStateFile)
	}

	nodeKeyFile := conf.NodeKeyFile()
	if fileExists(nodeKeyFile) {
		if _, err := p2p.LoadNodeKey(nodeKeyFile); err != nil {
			return nil, fmt.Errorf("invalid node key: %w", err)
		}

		log.Infow("Found node key", "path", nodeKeyFile)
	} else {
		nodePrivateKey, err := privateKeyFromStringOrFile(Parameters.Tendermint.NodePrivateKey)
		if err != nil {
			return nil, fmt.Errorf("failed to load node private key: %w", err)
		}
		nodeKey := p2p.NodeKey{PrivKey: tmed25519.PrivKey(nodePrivateKey)}
		if err := nodeKey.SaveAs(nodeKeyFile); err != nil {
			return nil, fmt.Errorf("failed to save key file: %w", err)
		}

		log.Infow("Generated node key", "path", nodeKeyFile)
	}

	genesisValidators := make([]tmtypes.GenesisValidator, 0, len(Parameters.Tendermint.Validators))
	for name, validator := range Parameters.Tendermint.Validators {
		var publicKeyBytes types.Byte32
		if err := publicKeyBytes.Set(validator.PublicKey); err != nil {
			return nil, fmt.Errorf("invalid public key for validator %s: %w", strconv.Quote(name), err)
		}
		pubKey := tmed25519.PubKey(publicKeyBytes[:])
		genesisValidators = append(genesisValidators, tmtypes.GenesisValidator{
			PubKey: pubKey,
			Power:  validator.Power,
			Name:   name,
		})
	}

	var peers []string
	id := p2p.PubKeyToID(tmConsensusKey.PubKey())
	for _, peer := range Parameters.Tendermint.Peers {
		addr, err := p2p.NewNetAddressString(peer)
		if err != nil {
			return nil, fmt.Errorf("invalid address in peers: %w", err)
		}

		// only add the address as a peer, if it does not belong to ourselves
		if id != addr.ID {
			peers = append(peers, peer)
		}
	}

	conf.P2P.PersistentPeers = strings.Join(peers, ",")
	// Forcing the max number of connections to be equal to the configured persistent peers (e.g., the entire committee)
	conf.P2P.MaxNumInboundPeers = len(peers)
	conf.P2P.MaxNumOutboundPeers = len(peers)
	// Disable Peer Exchange Reactor
	conf.P2P.PexReactor = false

	// Disable indexer (because it never gets pruned)
	// doc: `If you do not need to query transactions from the specific node, you can disable indexing.`
	conf.TxIndex = &tmconfig.TxIndexConfig{
		Indexer: "null",
	}

	// Prometheus configuration
	conf.Instrumentation.Prometheus = Parameters.Tendermint.Prometheus.Enabled
	conf.Instrumentation.PrometheusListenAddr = Parameters.Tendermint.Prometheus.BindAddress

	networkName := deps.NodeBridge.ProtocolParameters().NetworkName
	if Parameters.Tendermint.ChainID != networkName {
		return nil, fmt.Errorf("chain ID must match the network name %s", strconv.Quote(networkName))
	}

	genFile := conf.GenesisFile()
	if fileExists(genFile) {
		log.Infow("Found genesis file", "path", genFile)
	} else {
		gen := &tmtypes.GenesisDoc{
			GenesisTime:     time.Unix(Parameters.Tendermint.GenesisTime, 0),
			ChainID:         Parameters.Tendermint.ChainID,
			InitialHeight:   0,
			ConsensusParams: tmtypes.DefaultConsensusParams(),
			Validators:      genesisValidators,
		}
		gen.ConsensusParams.Block.TimeIotaMs = 500 // 0.5 s

		if err := gen.ValidateAndComplete(); err != nil {
			return nil, fmt.Errorf("invalid genesis config: %w", err)
		}
		if err := gen.SaveAs(genFile); err != nil {
			return nil, fmt.Errorf("failed to save genesis file: %w", err)
		}

		log.Infow("Generated genesis file", "path", genFile)
	}

	return conf, nil
}

func newTendermintNode(conf *tmconfig.Config) (*tmnode.Node, error) {
	pval := privval.LoadFilePV(conf.PrivValidatorKeyFile(), conf.PrivValidatorStateFile())
	nodeKey, err := p2p.LoadNodeKey(conf.NodeKeyFile())
	if err != nil {
		return nil, fmt.Errorf("failed to load node key %s: %w", conf.NodeKeyFile(), err)
	}
	// use a separate logger for Tendermint
	lvl, err := zapcore.ParseLevel(Parameters.Tendermint.LogLevel)
	if err != nil {
		return nil, fmt.Errorf("invalid log level: %w", err)
	}
	tenderLogger := NewTenderLogger(Component.App().NewLogger("Tendermint"), lvl)

	// start Tendermint, this replays blocks until Tendermint and Coordinator are synced
	node, err := tmnode.NewNode(conf,
		pval,
		nodeKey,
		proxy.NewLocalClientCreator(deps.DeCoo),
		tmnode.DefaultGenesisDocProviderFunc(conf),
		tmnode.DefaultDBProvider,
		tmnode.DefaultMetricsProvider(conf.Instrumentation),
		tenderLogger)
	if err != nil {
		return nil, err
	}

	return node, nil
}
