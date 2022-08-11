---
description: This section describes the configuration parameters and their types for INX-Tendercoo.
keywords:
- IOTA Node 
- Hornet Node
- Coordinator
- Configuration
- JSON
- Customize
- Config
- reference
---


# Core Configuration

INX-Tendercoo uses a JSON standard format as a config file. If you are unsure about JSON syntax, you can find more information in the [official JSON specs](https://www.json.org).

You can change the path of the config file by using the `-c` or `--config` argument while executing `inx-tendercoo` executable.

For example:
```bash
inx-tendercoo -c config_defaults.json
```

You can always get the most up-to-date description of the config parameters by running:

```bash
inx-tendercoo -h --full
```

## <a id="app"></a> 1. Application

| Name            | Description                                                                                            | Type    | Default value |
| --------------- | ------------------------------------------------------------------------------------------------------ | ------- | ------------- |
| checkForUpdates | Whether to check for updates of the application or not                                                 | boolean | true          |
| stopGracePeriod | The maximum time to wait for background processes to finish during shutdown before terminating the app | string  | "5m"          |

Example:

```json
  {
    "app": {
      "checkForUpdates": true,
      "stopGracePeriod": "5m"
    }
  }
```

## <a id="inx"></a> 2. INX

| Name    | Description                            | Type   | Default value    |
| ------- | -------------------------------------- | ------ | ---------------- |
| address | The INX address to which to connect to | string | "localhost:9029" |

Example:

```json
  {
    "inx": {
      "address": "localhost:9029"
    }
  }
```

## <a id="coordinator"></a> 3. Coordinator

| Name                                  | Description                                                     | Type   | Default value |
| ------------------------------------- | --------------------------------------------------------------- | ------ | ------------- |
| interval                              | The interval in which milestones are issued                     | string | "5s"          |
| maxTrackedBlocks                      | Maximum amount of blocks tracked by the milestone tip selection | int    | 10000         |
| [tipsel](#coordinator_tipsel)         | Configuration for tipsel                                        | object |               |
| [tendermint](#coordinator_tendermint) | Configuration for tendermint                                    | object |               |

### <a id="coordinator_tipsel"></a> Tipsel

| Name                     | Description                                                                         | Type   | Default value |
| ------------------------ | ----------------------------------------------------------------------------------- | ------ | ------------- |
| maxTips                  | Maximum amount of tips returned                                                     | int    | 7             |
| reducedConfirmationLimit | Only select tips that newly reference more than this limit compared to the best tip | float  | 0.5           |
| timeout                  | Timeout after which tip selection is cancelled                                      | string | "100ms"       |

### <a id="coordinator_tendermint"></a> Tendermint

| Name                                             | Description                                            | Type   | Default value     |
| ------------------------------------------------ | ------------------------------------------------------ | ------ | ----------------- |
| bindAddress                                      | Binding address to listen for incoming connections     | string | "0.0.0.0:26656"   |
| root                                             | Root directory for all Tendermint data                 | string | "tendermint"      |
| logLevel                                         | Root directory for all Tendermint data                 | string | "info"            |
| genesisTime                                      | Genesis time of the Tendermint blockchain in Unix time | int    | 0                 |
| chainID                                          | Human-readable ID of the Tendermint blockchain         | string | "tendercoo"       |
| [consensus](#coordinator_tendermint_consensus)   | Configuration for consensus                            | object |                   |
| [prometheus](#coordinator_tendermint_prometheus) | Configuration for prometheus                           | object |                   |
| validators                                       | Defines the Tendermint validators                      | object | see example below |

### <a id="coordinator_tendermint_consensus"></a> Consensus

| Name                      | Description                                                                  | Type    | Default value |
| ------------------------- | ---------------------------------------------------------------------------- | ------- | ------------- |
| createEmptyBlocks         | EmptyBlocks mode                                                             | boolean | false         |
| createEmptyBlocksInterval | Possible interval between empty blocks                                       | string  | "0s"          |
| blockInterval             | How long we wait after committing a block, before starting on the new height | string  | "1s"          |
| skipBlockTimeout          | Make progress as soon as we have all the precommits                          | boolean | false         |

### <a id="coordinator_tendermint_prometheus"></a> Prometheus

| Name        | Description                              | Type    | Default value     |
| ----------- | ---------------------------------------- | ------- | ----------------- |
| enabled     | Toggle for Prometheus metrics collection | boolean | false             |
| bindAddress | Prometheus listening address binding     | string  | "localhost:26660" |

Example:

```json
  {
    "coordinator": {
      "interval": "5s",
      "maxTrackedBlocks": 10000,
      "tipsel": {
        "maxTips": 7,
        "reducedConfirmationLimit": 0.5,
        "timeout": "100ms"
      },
      "tendermint": {
        "bindAddress": "0.0.0.0:26656",
        "root": "tendermint",
        "logLevel": "info",
        "genesisTime": 0,
        "chainID": "tendercoo",
        "consensus": {
          "createEmptyBlocks": false,
          "createEmptyBlocksInterval": "0s",
          "blockInterval": "1s",
          "skipBlockTimeout": false
        },
        "prometheus": {
          "enabled": false,
          "bindAddress": "localhost:26660"
        },
        "validators": null
      }
    }
  }
```

## <a id="profiling"></a> 4. Profiling

| Name        | Description                                       | Type    | Default value    |
| ----------- | ------------------------------------------------- | ------- | ---------------- |
| enabled     | Whether the profiling plugin is enabled           | boolean | false            |
| bindAddress | The bind address on which the profiler listens on | string  | "localhost:6060" |

Example:

```json
  {
    "profiling": {
      "enabled": false,
      "bindAddress": "localhost:6060"
    }
  }
```

