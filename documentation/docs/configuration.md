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

| Name              | Description                                            | Type    | Default value     |
| ----------------- | ------------------------------------------------------ | ------- | ----------------- |
| root              | Root directory for all Tendermint data                 | string  | "tendermint"      |
| logLevel          | Root directory for all Tendermint data                 | string  | "info"            |
| createEmptyBlocks | Root directory for all Tendermint data                 | boolean | false             |
| genesisTime       | Genesis time of the Tendermint blockchain in Unix time | int     | 0                 |
| chainID           | Human-readable ID of the Tendermint blockchain         | string  | "tendercoo"       |
| validators        | Defines the Tendermint validators                      | object  | see example below |

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
        "root": "tendermint",
        "logLevel": "info",
        "createEmptyBlocks": false,
        "genesisTime": 0,
        "chainID": "tendercoo",
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
