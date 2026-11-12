package mysqlproxy

import (
	"encoding/binary"
	"github.com/go-mysql-org/go-mysql/mysql"
	"io"
	"os"
)

type Parameter struct {
	Type     uint16 `json:"type"`
	Null     bool   `json:"null"`
	Bytes    int    `json:"bytes"`
	Streamed bool   `json:"streamed"`
}

func (p *preparedQuery) bindings(request []byte) ([]Parameter, error) {
	if len(request) < 10 || request[5]&^mysql.PARAMETER_COUNT_AVAILABLE != 0 || binary.LittleEndian.Uint32(request[6:10]) != 1 {
		return nil, ErrPacket
	}
	count := int(p.parameters)
	if count == 0 {
		if len(request) != 10 {
			return nil, ErrPacket
		}
		return []Parameter{}, nil
	}
	bitmapSize := (count + 7) / 8
	position := 10 + bitmapSize
	if len(request) <= position {
		return nil, ErrPacket
	}
	bitmap := request[10:position]
	flag := request[position]
	position++
	if flag > 1 {
		return nil, ErrPacket
	}
	if flag == 1 {
		if len(request)-position < count*2 {
			return nil, ErrPacket
		}
		p.types = make([]uint16, count)
		for i := range count {
			p.types[i] = binary.LittleEndian.Uint16(request[position : position+2])
			position += 2
			if p.types[i]&0x7f00 != 0 {
				return nil, ErrPacket
			}
		}
	}
	if len(p.types) != count {
		return nil, ErrPacket
	}
	out := make([]Parameter, count)
	for i, kind := range p.types {
		out[i] = Parameter{Type: kind, Null: bitmap[i/8]&(1<<uint(i%8)) != 0 || byte(kind) == mysql.MYSQL_TYPE_NULL}
		if out[i].Null {
			continue
		}
		if size, ok := p.longParameters[uint16(i)]; ok {
			out[i].Streamed = true
			out[i].Bytes = size
			continue
		}
		size := 0
		switch byte(kind) {
		case mysql.MYSQL_TYPE_TINY:
			size = 1
		case mysql.MYSQL_TYPE_SHORT, mysql.MYSQL_TYPE_YEAR:
			size = 2
		case mysql.MYSQL_TYPE_LONG, mysql.MYSQL_TYPE_INT24, mysql.MYSQL_TYPE_FLOAT:
			size = 4
		case mysql.MYSQL_TYPE_LONGLONG, mysql.MYSQL_TYPE_DOUBLE:
			size = 8
		case mysql.MYSQL_TYPE_DATE, mysql.MYSQL_TYPE_DATETIME, mysql.MYSQL_TYPE_TIMESTAMP, mysql.MYSQL_TYPE_TIME:
			if position >= len(request) {
				return nil, ErrPacket
			}
			size = int(request[position])
			position++
			if byte(kind) == mysql.MYSQL_TYPE_TIME {
				if size != 0 && size != 8 && size != 12 {
					return nil, ErrPacket
				}
			} else if size != 0 && size != 4 && size != 7 && size != 11 {
				return nil, ErrPacket
			}
		case mysql.MYSQL_TYPE_DECIMAL, mysql.MYSQL_TYPE_NEWDECIMAL, mysql.MYSQL_TYPE_STRING, mysql.MYSQL_TYPE_VAR_STRING, mysql.MYSQL_TYPE_VARCHAR, mysql.MYSQL_TYPE_BLOB, mysql.MYSQL_TYPE_TINY_BLOB, mysql.MYSQL_TYPE_MEDIUM_BLOB, mysql.MYSQL_TYPE_LONG_BLOB, mysql.MYSQL_TYPE_BIT, mysql.MYSQL_TYPE_JSON, mysql.MYSQL_TYPE_GEOMETRY:
			length, n, e := lengthEncoded(request[position:])
			if e != nil || length > MaxPacketBytes {
				return nil, ErrPacket
			}
			position += n
			size = int(length)
		default:
			return nil, ErrPacket
		}
		if size > len(request)-position {
			return nil, ErrPacket
		}
		out[i].Bytes = size
		position += size
	}
	if position != len(request) {
		return nil, ErrPacket
	}
	return out, nil
}
func (p *preparedQuery) spoolLongData(parameter uint16, data []byte) error {
	if p.spool == nil {
		file, e := os.CreateTemp(os.Getenv("NT_MYSQL_SPOOL_DIR"), "nt-mysql-long-*")
		if e != nil {
			return e
		}
		p.spool = file
		p.longParameters = map[uint16]int{}
	}
	var header [6]byte
	binary.LittleEndian.PutUint16(header[:2], parameter)
	binary.LittleEndian.PutUint32(header[2:], uint32(len(data)))
	if e := writeAll(p.spool, header[:]); e != nil {
		return e
	}
	if e := writeAll(p.spool, data); e != nil {
		return e
	}
	p.longParameters[parameter] += len(data)
	return nil
}
func (p *preparedQuery) discardLongData() {
	if p.spool != nil {
		name := p.spool.Name()
		p.spool.Close()
		os.Remove(name)
		p.spool = nil
	}
	p.longParameters = nil
	p.longBytes = 0
}
func (p *preparedQuery) replayLongData(target io.Writer, id uint32) error {
	if p.spool == nil {
		return nil
	}
	if _, e := p.spool.Seek(0, io.SeekStart); e != nil {
		return e
	}
	for {
		var header [6]byte
		_, e := io.ReadFull(p.spool, header[:])
		if e == io.EOF {
			return nil
		}
		if e != nil {
			return e
		}
		size := binary.LittleEndian.Uint32(header[2:])
		if size > MaxPacketBytes-7 {
			return ErrPacket
		}
		request := make([]byte, 7+int(size))
		request[0] = mysql.COM_STMT_SEND_LONG_DATA
		binary.LittleEndian.PutUint32(request[1:5], id)
		copy(request[5:7], header[:2])
		if _, e = io.ReadFull(p.spool, request[7:]); e != nil {
			return e
		}
		sequence := byte(0)
		if e = writePacket(target, &sequence, request); e != nil {
			return e
		}
		clear(request)
	}
}
