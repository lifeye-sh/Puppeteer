package mysqlproxy

import (
	"bytes"
	"encoding/json"
	"github.com/go-mysql-org/go-mysql/mysql"
	"os"
	"testing"
)

func TestTypedBindingsAreRedacted(t *testing.T) {
	p := preparedQuery{parameters: 2}
	header := []byte{mysql.COM_STMT_EXECUTE, 1, 0, 0, 0, 0, 1, 0, 0, 0, 2, 1}
	request := append(append([]byte{}, header...), mysql.MYSQL_TYPE_VAR_STRING, 0, mysql.MYSQL_TYPE_LONG, 0, 3, 'f', 'o', 'o')
	metadata, e := p.bindings(request)
	if e != nil || len(metadata) != 2 || metadata[0].Bytes != 3 || !metadata[1].Null {
		t.Fatal(metadata, e)
	}
	serialized, _ := json.Marshal(metadata)
	if bytes.Contains(serialized, []byte("foo")) {
		t.Fatal("parameter contents leaked")
	}
	request = append(request, 1)
	if _, e = p.bindings(request); e == nil {
		t.Fatal("trailing bytes accepted")
	}
	request = append([]byte{}, header...)
	request[5] = 1
	if _, e = p.bindings(request); e == nil {
		t.Fatal("unsupported cursor accepted")
	}
	if _, e = (&preparedQuery{parameters: 1}).bindings([]byte{mysql.COM_STMT_EXECUTE, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0}); e == nil {
		t.Fatal("missing type cache accepted")
	}
}
func TestLongDataSpoolReplayAndCleanup(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	p := preparedQuery{parameters: 1}
	defer p.discardLongData()
	if e := p.spoolLongData(0, []byte("private parameter")); e != nil {
		t.Fatal(e)
	}
	path := p.spool.Name()
	info, e := os.Stat(path)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("spool permissions", e)
	}
	request := []byte{mysql.COM_STMT_EXECUTE, 5, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, mysql.MYSQL_TYPE_BLOB, 0}
	metadata, e := p.bindings(request)
	if e != nil || !metadata[0].Streamed || metadata[0].Bytes != 17 {
		t.Fatal(metadata, e)
	}
	var wire bytes.Buffer
	if e = p.replayLongData(&wire, 5); e != nil {
		t.Fatal(e)
	}
	sequence := byte(0)
	data, e := readPacket(&wire, &sequence, MaxPacketBytes)
	if e != nil || data[0] != mysql.COM_STMT_SEND_LONG_DATA || data[1] != 5 || string(data[7:]) != "private parameter" {
		t.Fatal("replay mismatch", e)
	}
	p.discardLongData()
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("spool not removed", e)
	}
	if len(p.longParameters) != 0 {
		t.Fatal("long-data metadata retained")
	}
}
