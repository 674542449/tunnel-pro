package proto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"time"
)

const (
	CmdAuth byte = 0x01
	CmdOpen byte = 0x02
	CmdData byte = 0x03
	CmdAck  byte = 0x04
	CmdFin  byte = 0x05
	CmdPing   byte = 0x06
	CmdUserID byte = 0x07

	AtypIPv4   byte = 0x01
	AtypDomain byte = 0x03
	AtypIPv6   byte = 0x04

	StatusOK   byte = 0x00
	StatusFail byte = 0x01
)

const HeaderSize = 7 // stream_id(4) + type(1) + pad_len(2)

func MakeFrame(streamID uint32, cmd byte, payload []byte) []byte {
	padLen := calcPadding(cmd, len(payload))
	buf := make([]byte, HeaderSize+len(payload)+padLen)
	binary.BigEndian.PutUint32(buf[0:4], streamID)
	buf[4] = cmd
	binary.BigEndian.PutUint16(buf[5:7], uint16(padLen))
	copy(buf[HeaderSize:], payload)
	return buf
}

func EncodeDataFrame(buf []byte, streamID uint32, payload []byte) []byte {
	n := HeaderSize + len(payload)
	buf = buf[:n]
	binary.BigEndian.PutUint32(buf[0:4], streamID)
	buf[4] = CmdData
	binary.BigEndian.PutUint16(buf[5:7], 0)
	copy(buf[HeaderSize:], payload)
	return buf
}

func ParseFrame(data []byte) (streamID uint32, cmd byte, payload []byte, err error) {
	if len(data) < HeaderSize {
		return 0, 0, nil, errors.New("frame too short")
	}
	streamID = binary.BigEndian.Uint32(data[0:4])
	cmd = data[4]
	padLen := int(binary.BigEndian.Uint16(data[5:7]))
	end := len(data) - padLen
	if end < HeaderSize {
		return 0, 0, nil, errors.New("invalid padding")
	}
	return streamID, cmd, data[HeaderSize:end], nil
}

func calcPadding(cmd byte, payloadLen int) int {
	total := HeaderSize + payloadLen
	// 大数据帧：不填充，减少带宽浪费
	if cmd == CmdData && total >= 256 {
		return 0
	}
	// 小帧：填充到 200-250 字节，防止大小指纹
	if total < 200 {
		return 200 - total + rand.Intn(51)
	}
	// 中等帧：少量填充
	return rand.Intn(33)
}

func MakeAuthPayload(psk string) []byte {
	buf := make([]byte, 40)
	binary.BigEndian.PutUint64(buf[0:8], uint64(time.Now().Unix()))
	mac := hmac.New(sha256.New, []byte(psk))
	mac.Write(buf[0:8])
	copy(buf[8:40], mac.Sum(nil))
	return buf
}

func VerifyAuthPayload(data []byte, psk string) error {
	if len(data) != 40 {
		return errors.New("invalid auth payload")
	}
	ts := int64(binary.BigEndian.Uint64(data[0:8]))
	diff := time.Now().Unix() - ts
	if diff < 0 {
		diff = -diff
	}
	if diff > 300 {
		return errors.New("timestamp expired")
	}
	mac := hmac.New(sha256.New, []byte(psk))
	mac.Write(data[0:8])
	if !hmac.Equal(data[8:40], mac.Sum(nil)) {
		return errors.New("hmac mismatch")
	}
	return nil
}

func MakeConnectPayload(host string, port uint16) []byte {
	var buf []byte
	ip := net.ParseIP(host)
	if ip != nil {
		if v4 := ip.To4(); v4 != nil {
			buf = append(buf, AtypIPv4)
			buf = append(buf, v4...)
		} else {
			buf = append(buf, AtypIPv6)
			buf = append(buf, ip.To16()...)
		}
	} else {
		buf = append(buf, AtypDomain)
		buf = append(buf, byte(len(host)))
		buf = append(buf, host...)
	}
	p := make([]byte, 2)
	binary.BigEndian.PutUint16(p, port)
	return append(buf, p...)
}

func ParseConnectPayload(data []byte) (string, error) {
	if len(data) < 2 {
		return "", errors.New("connect payload too short")
	}
	atyp := data[0]
	var host string
	var off int

	switch atyp {
	case AtypIPv4:
		if len(data) < 7 {
			return "", errors.New("short ipv4")
		}
		host = net.IP(data[1:5]).String()
		off = 5
	case AtypDomain:
		if len(data) < 3 {
			return "", errors.New("short domain")
		}
		dl := int(data[1])
		if len(data) < 2+dl+2 {
			return "", errors.New("short domain data")
		}
		host = string(data[2 : 2+dl])
		off = 2 + dl
	case AtypIPv6:
		if len(data) < 19 {
			return "", errors.New("short ipv6")
		}
		host = net.IP(data[1:17]).String()
		off = 17
	default:
		return "", fmt.Errorf("unknown atyp %d", atyp)
	}

	if len(data) < off+2 {
		return "", errors.New("missing port")
	}
	port := binary.BigEndian.Uint16(data[off : off+2])
	return fmt.Sprintf("%s:%d", host, port), nil
}

// DynamicPath computes an HMAC-based path that rotates every 30 minutes.
func DynamicPath(psk string, offset int64) string {
	slot := time.Now().Unix()/1800 + offset
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(slot))
	mac := hmac.New(sha256.New, []byte(psk))
	mac.Write(buf)
	return "/" + hex.EncodeToString(mac.Sum(nil)[:8])
}

func IsValidPath(path, psk string) bool {
	for _, off := range []int64{0, -1, 1} {
		if path == DynamicPath(psk, off) {
			return true
		}
	}
	return false
}
