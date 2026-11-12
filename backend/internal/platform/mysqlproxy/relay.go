package mysqlproxy

import (
	"context"
	"encoding/binary"
	"errors"
	"github.com/go-mysql-org/go-mysql/mysql"
	"net"
	"os"
	"time"
)

type QueryEvent struct {
	Phase      string
	SQL        string
	Command    byte
	Rows       uint64
	Failed     bool
	Unknown    bool // transport failure after forwarding; target outcome is not known
	Duration   time.Duration
	Parameters []Parameter
}
type Relay struct {
	Downstream, Upstream net.Conn
	Database             string
	Check                func(context.Context) error
	Validate             func(string) error
	Audit                func(context.Context, QueryEvent) error
}
type preparedQuery struct {
	query          string
	longBytes      int
	parameters     uint16
	types          []uint16
	spool          *os.File
	longParameters map[uint16]int
}

func (r *Relay) Serve(ctx context.Context) error {
	if r.Check == nil || r.Validate == nil || r.Audit == nil {
		return errors.New("proxy policy is incomplete")
	}
	statements := map[uint32]*preparedQuery{}
	buffered := 0
	defer func() {
		for _, statement := range statements {
			statement.discardLongData()
		}
	}()
	stop := context.AfterFunc(ctx, func() { r.Downstream.Close(); r.Upstream.Close() })
	defer stop()
	for {
		r.Downstream.SetReadDeadline(time.Now().Add(time.Hour))
		downSeq := byte(0)
		request, e := readPacket(r.Downstream, &downSeq, MaxPacketBytes)
		if e != nil {
			return e
		}
		if len(request) == 0 {
			return ErrPacket
		}
		command := request[0]
		if command == mysql.COM_QUIT {
			return nil
		}
		if e = r.Check(ctx); e != nil {
			return e
		}
		var query string
		var prepared *preparedQuery
		var statementID uint32
		var parameters []Parameter
		allowed := true
		switch command {
		case mysql.COM_QUERY, mysql.COM_STMT_PREPARE:
			query = string(request[1:])
			allowed = r.Validate(query) == nil
		case mysql.COM_INIT_DB:
			allowed = string(request[1:]) == r.Database
		case mysql.COM_PING:
			allowed = len(request) == 1
		case mysql.COM_STMT_EXECUTE, mysql.COM_STMT_CLOSE, mysql.COM_STMT_RESET, mysql.COM_STMT_SEND_LONG_DATA:
			if len(request) < 5 {
				return ErrPacket
			}
			statementID = binary.LittleEndian.Uint32(request[1:5])
			prepared = statements[statementID]
			if command == mysql.COM_STMT_CLOSE {
				if len(request) != 5 {
					return ErrPacket
				}
				// Connector/J closes statement 0 after an unsupported PREPARE
				// before falling back to text execution. CLOSE has no response;
				// an unknown handle is a no-op, not a connection error.
				if prepared == nil {
					continue
				}
			}
			allowed = prepared != nil
			if prepared != nil {
				query = prepared.query
				allowed = r.Validate(query) == nil
			}
			if command == mysql.COM_STMT_EXECUTE && len(request) < 10 {
				return ErrPacket
			}
			if command == mysql.COM_STMT_EXECUTE && prepared != nil {
				parameters, e = prepared.bindings(request)
				if e != nil {
					return e
				}
			}
			if command == mysql.COM_STMT_SEND_LONG_DATA {
				if len(request) < 7 || prepared == nil {
					return ErrPacket
				}
				parameter := binary.LittleEndian.Uint16(request[5:7])
				prepared.longBytes += len(request) - 7
				buffered += len(request) - 7
				if !allowed || parameter >= prepared.parameters || prepared.longBytes > MaxPacketBytes || buffered > MaxPacketBytes {
					return ErrPacket
				}
				if e = prepared.spoolLongData(parameter, request[7:]); e != nil {
					return e
				}
				clear(request)
				continue
			}
		default:
			allowed = false
		}
		event := QueryEvent{SQL: query, Command: command, Phase: "start", Parameters: parameters}
		start := time.Now()
		// The durable start event is written before any query reaches the target.
		tracked := command == mysql.COM_QUERY || command == mysql.COM_STMT_EXECUTE || command == mysql.COM_STMT_PREPARE
		if tracked {
			if e = r.Audit(ctx, event); e != nil {
				return e
			}
		}
		r.Downstream.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if !allowed {
			event.Failed = true
			event.Phase = "finish"
			if tracked {
				event.Duration = time.Since(start)
				if e = r.Audit(ctx, event); e != nil {
					return e
				}
			}
			if command == mysql.COM_STMT_CLOSE || command == mysql.COM_STMT_SEND_LONG_DATA {
				return errors.New("invalid no-response command")
			}
			if e = writePacket(r.Downstream, &downSeq, append([]byte{0xff, 0x76, 0x04, '#', '4', '2', '0', '0', '0'}, []byte("Command is not allowed by the database access policy")...)); e != nil {
				return e
			}
			continue
		}
		r.Upstream.SetDeadline(time.Now().Add(3 * time.Minute))
		if command == mysql.COM_STMT_EXECUTE {
			if e = prepared.replayLongData(r.Upstream, statementID); e != nil {
				return e
			}
		}
		if prepared != nil && (command == mysql.COM_STMT_EXECUTE || command == mysql.COM_STMT_RESET || command == mysql.COM_STMT_CLOSE) {
			buffered -= prepared.longBytes
			prepared.discardLongData()
		}
		upSeq := byte(0)
		if e = writePacket(r.Upstream, &upSeq, request); e != nil {
			return e
		}
		clear(request)
		switch command {
		case mysql.COM_STMT_CLOSE:
			delete(statements, statementID)
			continue
		case mysql.COM_STMT_SEND_LONG_DATA:
			continue
		case mysql.COM_STMT_PREPARE:
			var response []byte
			response, e = readPacket(r.Upstream, &upSeq, MaxPacketBytes)
			if e != nil {
				return e
			}
			if len(response) == 0 {
				return ErrPacket
			}
			if response[0] == 0xff {
				event.Failed = true
				e = writePacket(r.Downstream, &downSeq, response)
			} else {
				if len(response) < 12 || response[0] != 0 {
					return ErrPacket
				}
				id := binary.LittleEndian.Uint32(response[1:5])
				columns := binary.LittleEndian.Uint16(response[5:7])
				parameters := binary.LittleEndian.Uint16(response[7:9])
				if len(statements) >= 1024 || columns > 4096 || parameters > 4096 {
					return ErrPacket
				}
				statements[id] = &preparedQuery{query: query, parameters: parameters}
				e = writePacket(r.Downstream, &downSeq, response)
				for _, count := range []uint16{parameters, columns} {
					if e != nil {
						break
					}
					if count > 0 {
						for i := 0; i < int(count)+1; i++ {
							response, e = readPacket(r.Upstream, &upSeq, MaxPacketBytes)
							if e != nil {
								break
							}
							e = writePacket(r.Downstream, &downSeq, response)
							if e != nil {
								break
							}
						}
					}
				}
			}
		default:
			event.Rows, event.Failed, e = r.results(&upSeq, &downSeq)
		}
		if tracked {
			event.Phase = "finish"
			event.Duration = time.Since(start)
			event.Failed = event.Failed || e != nil
			event.Unknown = e != nil
			if auditErr := r.Audit(ctx, event); auditErr != nil {
				return auditErr
			}
		}
		if e != nil {
			return e
		}
	}
}

// Results are forwarded packet by packet. There is no result-sized allocation;
// blocked client writes stop upstream reads and naturally apply backpressure.
func (r *Relay) results(upSeq, downSeq *byte) (rows uint64, failed bool, err error) {
	forward := func(allowNull bool) ([]byte, error) {
		data, e := readPacket(r.Upstream, upSeq, MaxPacketBytes)
		if e != nil {
			return nil, e
		}
		if len(data) == 0 || (!allowNull && data[0] == 0xfb) {
			return nil, ErrPacket
		}
		r.Downstream.SetWriteDeadline(time.Now().Add(30 * time.Second))
		e = writePacket(r.Downstream, downSeq, data)
		return data, e
	}
	for {
		first, e := forward(false)
		if e != nil {
			return rows, true, e
		}
		if first[0] == 0xff {
			return rows, true, nil
		}
		var status uint16
		if first[0] == 0 {
			affected, n, e := lengthEncoded(first[1:])
			if e != nil {
				return rows, true, e
			}
			_, m, e := lengthEncoded(first[1+n:])
			if e != nil || len(first) < 1+n+m+4 {
				return rows, true, ErrPacket
			}
			status = binary.LittleEndian.Uint16(first[1+n+m:])
			rows += affected
		} else {
			columns, _, e := lengthEncoded(first)
			if e != nil || columns == 0 || columns > 4096 {
				return rows, true, ErrPacket
			}
			for i := uint64(0); i < columns; i++ {
				if _, e = forward(false); e != nil {
					return rows, true, e
				}
			}
			eof, e := forward(false)
			if e != nil {
				return rows, true, e
			}
			if len(eof) != 5 || eof[0] != 0xfe {
				return rows, true, ErrPacket
			}
			for {
				row, e := forward(true)
				if e != nil {
					return rows, true, e
				}
				if row[0] == 0xff {
					return rows, true, nil
				}
				if row[0] == 0xfe && len(row) < 9 {
					if len(row) != 5 {
						return rows, true, ErrPacket
					}
					status = binary.LittleEndian.Uint16(row[3:])
					break
				}
				rows++
			}
		}
		if status&mysql.SERVER_MORE_RESULTS_EXISTS == 0 {
			return rows, false, nil
		}
	}
}
