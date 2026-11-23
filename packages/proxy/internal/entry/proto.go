package entry

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// SOCKS5 reply codes used by the server side.
const (
	socksSucceeded         = 0x00
	socksGeneralFailure    = 0x01
	socksNotAllowed        = 0x02
	socksHostUnreachable   = 0x04
	socksCommandNotSupport = 0x07
	socksAddressNotSupport = 0x08
)

var errSocksAuth = errors.New("socks5 authentication failed")

// socksServerHandshake reads the greeting, the username/password exchange and
// the CONNECT request. Only username/password auth and CONNECT are offered.
// The caller answers the request with socksReply once the upstream is known.
func socksServerHandshake(reader *bufio.Reader, writer io.Writer, authenticate func(user, pass string) error) (string, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return "", err
	}
	if header[0] != 0x05 {
		return "", fmt.Errorf("socks version %d", header[0])
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return "", err
	}
	offered := false
	for _, method := range methods {
		if method == 0x02 {
			offered = true
		}
	}
	if !offered {
		_, _ = writer.Write([]byte{0x05, 0xff})
		return "", errSocksAuth
	}
	if _, err := writer.Write([]byte{0x05, 0x02}); err != nil {
		return "", err
	}
	user, pass, err := readSocksCredentials(reader)
	if err != nil {
		return "", err
	}
	if err := authenticate(user, pass); err != nil {
		_, _ = writer.Write([]byte{0x01, 0x01})
		return "", err
	}
	if _, err := writer.Write([]byte{0x01, 0x00}); err != nil {
		return "", err
	}
	request := make([]byte, 3)
	if _, err := io.ReadFull(reader, request); err != nil {
		return "", err
	}
	if request[0] != 0x05 {
		return "", fmt.Errorf("socks request version %d", request[0])
	}
	target, err := readSocksAddress(reader)
	if err != nil {
		socksReply(writer, socksAddressNotSupport)
		return "", err
	}
	if request[1] != 0x01 {
		socksReply(writer, socksCommandNotSupport)
		return "", fmt.Errorf("socks command %d is not supported", request[1])
	}
	return target, nil
}

func readSocksCredentials(reader *bufio.Reader) (string, string, error) {
	version, err := reader.ReadByte()
	if err != nil {
		return "", "", err
	}
	if version != 0x01 {
		return "", "", fmt.Errorf("socks auth version %d", version)
	}
	user, err := readShortString(reader)
	if err != nil {
		return "", "", err
	}
	pass, err := readShortString(reader)
	return user, pass, err
}

func readShortString(reader *bufio.Reader) (string, error) {
	size, err := reader.ReadByte()
	if err != nil {
		return "", err
	}
	buffer := make([]byte, int(size))
	_, err = io.ReadFull(reader, buffer)
	return string(buffer), err
}

func readSocksAddress(reader *bufio.Reader) (string, error) {
	kind, err := reader.ReadByte()
	if err != nil {
		return "", err
	}
	var host string
	switch kind {
	case 0x01:
		buffer := make([]byte, 4)
		if _, err := io.ReadFull(reader, buffer); err != nil {
			return "", err
		}
		host = net.IP(buffer).String()
	case 0x04:
		buffer := make([]byte, 16)
		if _, err := io.ReadFull(reader, buffer); err != nil {
			return "", err
		}
		host = net.IP(buffer).String()
	case 0x03:
		if host, err = readShortString(reader); err != nil {
			return "", err
		}
	default:
		return "", fmt.Errorf("socks address type %d", kind)
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(reader, portBytes); err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(portBytes)))), nil
}

// socksReply answers a CONNECT request with a zero bound address.
func socksReply(writer io.Writer, code byte) {
	_, _ = writer.Write([]byte{0x05, code, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
}

// socksClientConnect asks an upstream SOCKS5 server, already connected on conn,
// to CONNECT to target with username/password auth.
func socksClientConnect(conn net.Conn, user, pass, target string) error {
	if len(user) > 255 || len(pass) > 255 {
		return errors.New("socks credentials exceed 255 bytes")
	}
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("invalid target port %q", portText)
	}
	if _, err := conn.Write([]byte{0x05, 0x01, 0x02}); err != nil {
		return err
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil {
		return err
	}
	if reply[0] != 0x05 || reply[1] != 0x02 {
		return errors.New("upstream socks5 refused username/password auth")
	}
	auth := append([]byte{0x01, byte(len(user))}, user...)
	auth = append(append(auth, byte(len(pass))), pass...)
	if _, err := conn.Write(auth); err != nil {
		return err
	}
	if _, err := io.ReadFull(conn, reply); err != nil {
		return err
	}
	if reply[1] != 0x00 {
		return errors.New("upstream socks5 rejected the credentials")
	}
	request := []byte{0x05, 0x01, 0x00}
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		request = append(append(request, 0x01), ip.To4()...)
	} else if ip != nil {
		request = append(append(request, 0x04), ip.To16()...)
	} else {
		if len(host) > 255 {
			return errors.New("target host exceeds 255 bytes")
		}
		request = append(append(request, 0x03, byte(len(host))), host...)
	}
	request = binary.BigEndian.AppendUint16(request, uint16(port))
	if _, err := conn.Write(request); err != nil {
		return err
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil {
		return err
	}
	if head[1] != 0x00 {
		return fmt.Errorf("upstream socks5 connect failed with code %d", head[1])
	}
	skip := 0
	switch head[3] {
	case 0x01:
		skip = 4
	case 0x04:
		skip = 16
	case 0x03:
		size := make([]byte, 1)
		if _, err := io.ReadFull(conn, size); err != nil {
			return err
		}
		skip = int(size[0])
	default:
		return fmt.Errorf("upstream socks5 bound address type %d", head[3])
	}
	_, err = io.ReadFull(conn, make([]byte, skip+2))
	return err
}

// httpClientConnect asks an upstream HTTP proxy, already connected on conn, to
// open a CONNECT tunnel to target. Bytes the proxy sent after its response are
// handed back through the returned conn.
func httpClientConnect(conn net.Conn, user, pass, target string) (net.Conn, error) {
	request := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
	if user != "" || pass != "" {
		request += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass)) + "\r\n"
	}
	request += "\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return nil, err
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream http proxy answered %s", response.Status)
	}
	if reader.Buffered() == 0 {
		return conn, nil
	}
	return &bufferedConn{Conn: conn, reader: reader}, nil
}

// parseProxyAuthorization reads a Basic Proxy-Authorization header.
func parseProxyAuthorization(header string) (string, string, bool) {
	scheme, value, found := strings.Cut(strings.TrimSpace(header), " ")
	if !found || !strings.EqualFold(scheme, "basic") {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return "", "", false
	}
	user, pass, found := strings.Cut(string(decoded), ":")
	return user, pass, found
}

// bufferedConn serves bytes a bufio.Reader already pulled off the wire first.
type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(buffer []byte) (int, error) {
	return c.reader.Read(buffer)
}

// CloseWrite half-closes the wrapped connection when it supports that.
func (c *bufferedConn) CloseWrite() error {
	if closer, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return closer.CloseWrite()
	}
	return c.Conn.Close()
}
