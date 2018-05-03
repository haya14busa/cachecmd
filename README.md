# cachecmd

cachecmd runs a given command and caches the result of the command.
Return cached result instead if cache found.

## Installation

```shell
go get -u github.com/haya14busa/cachecmd/cmd/cachecmd
```

## Example

```shell
$ cachecmd -ttl=10s date +%S
14 # First run
$ sleep 5s
$ cachecmd -ttl=10s date +%S
14 # Read from cache
$ sleep 5s
$ cachecmd -ttl=10s date +%S
24 # cache is expired. Run command again and update cache.

# Force update: set -ttl=0
$ cachecmd -ttl=0 date +%S

# TTL is 10 min. Return cache result immediately from cache and update cache
# in background for every run.
$ cachecmd -ttl=10m -async sh -c 'date +%s; sleep 3s

# Cache result by current directory.
$ cachecmd -ttl=10m -key="$(pwd)" go list ./...
# https://github.com/github/hub
$ cachecmd -ttl=10m -key="$(pwd)" -async hub issue
```

## :bird: Author
haya14busa (https://github.com/haya14busa)

