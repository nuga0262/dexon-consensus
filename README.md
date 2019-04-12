[![CircleCI](https://circleci.com/gh/dexon-foundation/dexon-consensus.svg?style=svg&circle-token=faed911ec07618dfbd6868b09181aa2046b550d8)](https://circleci.com/gh/dexon-foundation/dexon-consensus)

DEXON Consensus
====================

## Getting Started
### Prerequisites

- [Go 1.10](https://golang.org/dl/) or a newer version
- [dep](https://github.com/golang/dep#installation) as dependency management

### Installation

1. Clone the repo
    ```
    git clone https://github.com/dexon-foundation/dexon-consensus.git
    cd dexon-consensus
    ```

2. Setup GOPATH, the GOPATH could be anywhere in the system. Here we use `$HOME/go`:
   ```
   export GOPATH=$HOME/go
   export PATH=$GOPATH/bin:$PATH
   ```
   You should write these settings to your `.bashrc` file.


3. Install go dependency management tool
   ```
   ./bin/install_tools.sh
   ```

4. Install all dependencies
   ```
   dep ensure
   ```

### Run Unit Tests

```
make pre-submit
```

## Simulation

### Simulation with Nodes connected by HTTP

1. Setup the configuration under `./test.toml`
2. Compile and install the cmd `dexon-simulation`

```
make
```

3. Run simulation:

```
dexcon-simulation -config test.toml -init
```

### Simulation with test.Scheduler

1. Setup the configuration under `./test.toml`
2. Compile and install the cmd `dexon-simulation-with-scheduler`

```
make
```

3. Run simulation with scheduler:

```
dexcon-simulation-with-scheduler -config test.toml
```

## License

DEXON Consensus is licensed under the
[GNU LGPL v3](https://www.gnu.org/licenses/lgpl-3.0.en.html),
or any later version at your option. The license text is included as `LICENSE`
in the repository. Since the GNU LGPL v3 itself is not a complete license but
a set of terms applied on top of the GNU GPL v3, the text of the
[GNU GPL v3](https://www.gnu.org/licenses/gpl-3.0.en.html)
is also included in the repository as `LICENSE.GPL`.
