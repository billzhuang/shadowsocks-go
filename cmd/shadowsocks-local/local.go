package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	ss "github.com/shadowsocks/shadowsocks-go/shadowsocks"
	"io"
	"log"
	"net"
	"os"
	"path"
	"strconv"
)

var debug ss.DebugLog

var (
	errAddrType      = errors.New("socks addr type not supported")
	errVer           = errors.New("socks version not supported")
	errMethod        = errors.New("socks only support 1 method now")
	errAuthExtraData = errors.New("socks authentication get extra data")
	errReqExtraData  = errors.New("socks request get extra data")
	errCmd           = errors.New("socks command not supported")
)

const (
	socksVer5       = 5
	socksCmdConnect = 1
)

func handShake(conn net.Conn) (err error) {
	const (
		idVer     = 0
		idNmethod = 1
	)
	// version identification and method selection message in theory can have
	// at most 256 methods, plus version and nmethod field in total 258 bytes
	// the current rfc defines only 3 authentication methods (plus 2 reserved),
	// so it won't be such long in practice

	buf := make([]byte, 258, 258)

	var n int
	// make sure we get the nmethod field
	if n, err = io.ReadAtLeast(conn, buf, idNmethod+1); err != nil {
		return
	}
	if buf[idVer] != socksVer5 {
		return errVer
	}
	nmethod := int(buf[idNmethod])
	msgLen := nmethod + 2
	if n == msgLen { // handshake done, common case
		// do nothing, jump directly to send confirmation
	} else if n < msgLen { // has more methods to read, rare case
		if _, err = io.ReadFull(conn, buf[n:msgLen]); err != nil {
			return
		}
	} else { // error, should not get extra data
		return errAuthExtraData
	}
	// send confirmation: version 5, no authentication required
	_, err = conn.Write([]byte{socksVer5, 0})
	return
}

func getRequest(conn net.Conn) (rawaddr []byte, host string, err error) {
	const (
		idVer   = 0
		idCmd   = 1
		idType  = 3 // address type index
		idIP0   = 4 // ip addres start index
		idDmLen = 4 // domain address length index
		idDm0   = 5 // domain address start index

		typeIP = 1 // type is ip address
		typeDm = 3 // type is domain address

		lenIP     = 3 + 1 + 4 + 2 // 3(ver+cmd+rsv) + 1addrType + 4ip + 2port
		lenDmBase = 3 + 1 + 1 + 2 // 3 + 1addrType + 1addrLen + 2port, plus addrLen
	)
	// refer to getRequest in server.go for why set buffer size to 263
	buf := make([]byte, 263, 263)
	var n int
	// read till we get possible domain length field
	if n, err = io.ReadAtLeast(conn, buf, idDmLen+1); err != nil {
		return
	}
	// check version and cmd
	if buf[idVer] != socksVer5 {
		err = errVer
		return
	}
	if buf[idCmd] != socksCmdConnect {
		err = errCmd
		return
	}

	reqLen := lenIP
	if buf[idType] == typeDm {
		reqLen = int(buf[idDmLen]) + lenDmBase
	} else if buf[idType] != typeIP {
		err = errAddrType
		return
	}

	if n == reqLen {
		// common case, do nothing
	} else if n < reqLen { // rare case
		if _, err = io.ReadFull(conn, buf[n:reqLen]); err != nil {
			return
		}
	} else {
		err = errReqExtraData
		return
	}

	rawaddr = buf[idType:reqLen]

	if debug {
		if buf[idType] == typeDm {
			host = string(buf[idDm0 : idDm0+buf[idDmLen]])
		} else if buf[idType] == typeIP {
			addrIp := make(net.IP, 4)
			copy(addrIp, buf[idIP0:idIP0+4])
			host = addrIp.String()
		}
		var port int16
		sb := bytes.NewBuffer(buf[reqLen-2 : reqLen])
		binary.Read(sb, binary.BigEndian, &port)
		host += ":" + strconv.Itoa(int(port))
	}

	return
}

type ServerEnctbl struct {
	server string
	enctbl *ss.EncryptTable
}

var servers struct {
	srvenc []*ServerEnctbl
	idx    uint8
}

func initServers(config *ss.Config) {
	if len(config.ServerPassword) == 0 {
		// only one encryption table
		enctbl := ss.GetTable(config.Password)
		srvPort := strconv.Itoa(config.ServerPort)
		srvArr := config.GetServerArray()
		n := len(srvArr)
		servers.srvenc = make([]*ServerEnctbl, n, n)

		for i, s := range srvArr {
			if ss.HasPort(s) {
				log.Println("ignore server_port option for server", s)
				servers.srvenc[i] = &ServerEnctbl{s, enctbl}
			} else {
				servers.srvenc[i] = &ServerEnctbl{s + ":" + srvPort, enctbl}
			}
		}
	} else {
		n := len(config.ServerPassword)
		servers.srvenc = make([]*ServerEnctbl, n, n)

		tblCache := make(map[string]*ss.EncryptTable)
		i := 0
		for s, passwd := range config.ServerPassword {
			if !ss.HasPort(s) {
				log.Fatal("no port for server %s, please specify port in the form of %s:port", s, s)
			}
			tbl, ok := tblCache[passwd]
			if !ok {
				tbl = ss.GetTable(passwd)
				tblCache[passwd] = tbl
			}
			servers.srvenc[i] = &ServerEnctbl{s, tbl}
			i++
		}
	}
	for _, se := range servers.srvenc {
		log.Println("available remote server", se.server)
	}
	return
}

// select one server to connect in round robin order
func createServerConn(rawaddr []byte, addr string) (remote *ss.Conn, err error) {
	n := len(servers.srvenc)
	if n == 1 {
		se := servers.srvenc[0]
		debug.Printf("connecting to %s via %s\n", addr, se.server)
		return ss.DialWithRawAddr(rawaddr, se.server, se.enctbl)
	}

	id := servers.idx
	servers.idx++ // it's ok for concurrent update
	for i := 0; i < n; i++ {
		se := servers.srvenc[(int(id)+i)%n]
		remote, err = ss.DialWithRawAddr(rawaddr, se.server, se.enctbl)
		if err == nil {
			debug.Printf("connected to %s via %s\n", addr, se.server)
			return
		} else {
			log.Println("error connecting to shadowsocks server:", err)
		}
	}
	return
}

func handleConnection(conn net.Conn) {
	if debug {
		debug.Printf("socks connect from %s\n", conn.RemoteAddr().String())
	}
	defer conn.Close()

	var err error = nil
	if err = handShake(conn); err != nil {
		log.Println("socks handshake:", err)
		return
	}
	rawaddr, addr, err := getRequest(conn)
	if err != nil {
		log.Println("error getting request:", err)
		return
	}
	// Sending connection established message immediately to client.
	// This some round trip time for creating socks connection with the client.
	// But if connection failed, the client will get connection reset error.
	_, err = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x08, 0x43})
	if err != nil {
		debug.Println("send connection confirmation:", err)
		return
	}

	remote, err := createServerConn(rawaddr, addr)
	if err != nil {
		if len(servers.srvenc) > 1 {
			log.Println("Failed connect to all avaiable shadowsocks server")
		}
		return
	}
	defer remote.Close()

	c := make(chan byte, 2)
	go ss.Pipe(conn, remote, c)
	go ss.Pipe(remote, conn, c)
	<-c // close the other connection whenever one connection is closed
	debug.Println("closing")
}

func run(port string) {
	ln, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("starting local socks5 server at port %v ...\n", port)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println("accept:", err)
			continue
		}
		go handleConnection(conn)
	}
}

func enoughOptions(config *ss.Config) bool {
	return config.Server != nil && config.ServerPort != 0 &&
		config.LocalPort != 0 && config.Password != ""
}

func main() {
	var configFile, cmdServer string
	var cmdConfig ss.Config
	var printVer bool

	flag.BoolVar(&printVer, "version", false, "print version")
	flag.StringVar(&configFile, "c", "config.json", "specify config file")
	flag.StringVar(&cmdServer, "s", "", "server address")
	flag.StringVar(&cmdConfig.Password, "k", "", "password")
	flag.IntVar(&cmdConfig.ServerPort, "p", 0, "server port")
	flag.IntVar(&cmdConfig.LocalPort, "l", 0, "local socks5 proxy port")
	flag.BoolVar((*bool)(&debug), "d", false, "print debug message")

	flag.Parse()

	if printVer {
		ss.PrintVersion()
		os.Exit(0)
	}

	cmdConfig.Server = cmdServer
	ss.SetDebug(debug)

	exists, err := ss.IsFileExists(configFile)
	// If no config file in current directory, try search it in the binary directory
	// Note there's no portable way to detect the binary directory.
	binDir := path.Dir(os.Args[0])
	if (!exists || err != nil) && binDir != "" && binDir != "." {
		oldConfig := configFile
		configFile = path.Join(binDir, "config.json")
		log.Printf("%s not found, try config file %s\n", oldConfig, configFile)
	}

	config, err := ss.ParseConfig(configFile)
	if err != nil {
		config = &cmdConfig
		if os.IsNotExist(err) {
			log.Println("config file not found, using all options from command line")
		} else {
			log.Printf("error reading config file: %v\n", err)
			os.Exit(1)
		}
	} else {
		ss.UpdateConfig(config, &cmdConfig)
	}

	if len(config.ServerPassword) == 0 {
		if !enoughOptions(config) {
			log.Println("must specify server address, password and both server/local port")
			os.Exit(1)
		}
	} else {
		if config.Password != "" || config.ServerPort != 0 || config.GetServerArray() != nil {
			log.Println("given server_password, ignore server, server_port and password option:", config)
		}
		if config.LocalPort == 0 {
			log.Fatal("must specify local port")
		}
	}

	initServers(config)

	run(strconv.Itoa(config.LocalPort))
}
