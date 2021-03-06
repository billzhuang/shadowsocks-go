# shadowsocks-go #

shadowsocks-go is a lightweight tunnel proxy which can help you get through firewalls. It is a port of [shadowsocks](https://github.com/clowwindy/shadowsocks).

The protocol is compatible with the origin shadowsocks (if both have been upgraded to the latest version).

# Install #

Compiled client binaries are provided on [google code](http://code.google.com/p/shadowsocks-go/downloads/list).

You can also install from source (assume you have go installed):

On server, run

```
go get github.com/shadowsocks/shadowsocks-go/cmd/shadowsocks-server
```

On client, run

```
go get github.com/shadowsocks/shadowsocks-go/cmd/shadowsocks-local
```

# Usage #

Both the server and client program will look for `config.json` in the current directory. You can use `-c` option to specify another configuration file.

Configuration file is in json format and has the same syntax with [shadowsocks-nodejs](https://github.com/clowwindy/shadowsocks-nodejs/). You can download the sample [`config.json`](https://github.com/shadowsocks/shadowsocks-go/blob/master/config.json), change the following values:

```
server          your server ip or hostname
server_port     server port
local_port      local socks5 proxy port
password        a password used to encrypt transfer
timeout         server option, in seconds
```

Run `shadowsocks-server` on your server. To run it in the background, run `shadowsocks-server > log &`.

On client, run `shadowsocks-local`. Change proxy settings of your browser to

```
SOCKS5 127.0.0.1:local_port
```

## Command line options ##

Command line options can override settings from configuration files.

```
shadowsocks-local -s server_name -p server_port -l local_port -k password -c config.json
shadowsocks-server -p server_port -k password -t timeout -c config.json
```

Use `-d` option to enable debug message.


## Use multiple servers on client

```
server_password    specify multiple server and password, server should be in the form of host:port
```

Here's a sample configuration [`client-multi-server.json`](https://github.com/shadowsocks/shadowsocks-go/blob/master/sample-config/client-multi-server.json). Given `server_password`, client program will ignore `server_port`, `server` and `password` options.

Servers are chosen in round robin fasion. If a server can't be connected, the client will try the next one. The client does not try to detect connection problems caused by incorrect password, this is intended for the user to notice the error.

## Multiple users with different passwords on server

The server can support users with different passwords. Each user will be served by a unique port. Use the following options on the server for such setup:

```
port_password   specify multiple ports and passwords to support multiple users
cache_enctable  store computed encryption table on disk to speedup server startup
```

Here's a sample configuration [`server-multi-port.json`](https://github.com/shadowsocks/shadowsocks-go/blob/master/sample-config/server-multi-port.json). Given `port_password`, server program will ignore `server_port` and `password` options.

Enabling `cache_enctable` is recommended if you have more than 20 different passwords. Unused password will not be deleted, so you may need to delete the file `table.cache` if it grows too big.

### Update port password for a running server  ###

Edit the config file used to start the server, then send `SIGHUP` to the server process.
