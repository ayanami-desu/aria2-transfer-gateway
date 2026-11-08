package ed2k

import (
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestParseTextList(t *testing.T) {
	got := ParseList([]byte("\ufeff# comment\r\n 1.2.3.4:4661 , 5.6.7.8:4725\n1.2.3.4:4661\ned2k://|server|9.9.9.9|4661|/\nbad-entry\n"))
	want := []string{"1.2.3.4:4661", "5.6.7.8:4725", "9.9.9.9:4661"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseList() = %#v, want %#v", got, want)
	}
}

func TestParseServerMet(t *testing.T) {
	data := buildServerMet(
		serverMetEntry{ip: [4]byte{176, 123, 5, 89}, port: 4725, name: "eMule Sunrise"},
		serverMetEntry{ip: [4]byte{91, 208, 162, 87}, port: 4232, name: "Sharing-Devils"},
	)
	got := ParseList(data)
	want := []string{"176.123.5.89:4725", "91.208.162.87:4232"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseList(server.met) = %#v, want %#v", got, want)
	}
}

func TestParseListFallsBackToTextOnBrokenServerMet(t *testing.T) {
	got := ParseList([]byte{0xe0, 0x05, 0x00})
	if len(got) != 0 {
		t.Fatalf("ParseList(short binary) = %#v, want empty", got)
	}
}

func TestSourceRefresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("1.2.3.4:4661\n# ignored\n5.6.7.8:4725"))
	}))
	defer server.Close()
	source := NewSource(server.URL, server.Client())
	count, err := source.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || !reflect.DeepEqual(source.List(), []string{"1.2.3.4:4661", "5.6.7.8:4725"}) {
		t.Fatalf("refresh = %d, %#v", count, source.List())
	}
}

func TestSourceRefreshRejectsEmptyList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("# no servers\n"))
	}))
	defer server.Close()
	source := NewSource(server.URL, server.Client())
	if _, err := source.Refresh(context.Background()); err == nil {
		t.Fatal("Refresh() succeeded with an empty list")
	}
}

type serverMetEntry struct {
	ip   [4]byte
	port uint16
	name string
}

func buildServerMet(entries ...serverMetEntry) []byte {
	data := []byte{0xe0}
	count := make([]byte, 4)
	binary.LittleEndian.PutUint32(count, uint32(len(entries)))
	data = append(data, count...)
	for _, entry := range entries {
		data = append(data, entry.ip[:]...)
		port := make([]byte, 2)
		binary.LittleEndian.PutUint16(port, entry.port)
		data = append(data, port...)
		data = append(data, 1, 0, 0, 0) // one tag
		data = append(data, 0x82, 0x01) // name ID 1, string value
		name := make([]byte, 2)
		binary.LittleEndian.PutUint16(name, uint16(len(entry.name)))
		data = append(data, name...)
		data = append(data, entry.name...)
	}
	return data
}
