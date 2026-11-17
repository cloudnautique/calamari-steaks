package bridge

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

var rfbHandshake = []byte("RFB 003.008\n\x01\x01")
var rfbKey = []byte{4, 1, 0, 0, 0, 0, 0, 0x61}
var rfbPointer = []byte{5, 1, 0, 10, 0, 20}
var rfbUpdate = []byte{3, 1, 0, 0, 0, 0, 0, 100, 0, 100}

func TestRFBFilterClassifiesMessagesAcrossBatches(t *testing.T) {
	stream := bytes.Join([][]byte{rfbHandshake, {2, 0, 0, 2, 0, 0, 0, 0, 0xff, 0xff, 0xff, 0x21}, rfbUpdate, rfbKey, rfbPointer, {5, 0x81, 0, 1, 0, 2, 1}, {6, 0, 0, 0, 0, 0, 0, 3, 'a', 'b', 'c'}, {6, 0, 0, 0, 0xff, 0xff, 0xff, 0xfe, 1, 2}, {255, 0, 0, 1, 0, 0, 0, 0x61, 0, 0, 0, 30}, {251, 0, 0, 10, 0, 10, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, {248, 0, 0, 0, 0, 0, 0, 1, 2, 9, 9}}, nil)
	wantInput := []bool{false, false, false, false, false, true, true, true, true, true, true, true, false}
	// Byte-at-a-time delivery must yield the same messages as one batch.
	for _, step := range []int{1, 3, len(stream)} {
		var filter rfbFilter
		var got []bool
		var joined []byte
		for offset := 0; offset < len(stream); offset += step {
			end := min(offset+step, len(stream))
			segments, err := filter.feed(stream[offset:end])
			if err != nil {
				t.Fatalf("step %d: %v", step, err)
			}
			for _, segment := range segments {
				got = append(got, segment.input)
				joined = append(joined, segment.data...)
			}
		}
		if !bytes.Equal(joined, stream) || len(filter.pending) != 0 {
			t.Fatalf("step %d: bytes were lost or reordered", step)
		}
		if len(got) != len(wantInput) {
			t.Fatalf("step %d: got %d messages, want %d", step, len(got), len(wantInput))
		}
		for i := range got {
			if got[i] != wantInput[i] {
				t.Fatalf("step %d: message %d input=%t, want %t", step, i, got[i], wantInput[i])
			}
		}
	}
}

func TestRFBFilterFailsClosed(t *testing.T) {
	for name, stream := range map[string][]byte{
		"old version":      []byte("RFB 003.003\n"),
		"unknown security": []byte("RFB 003.008\n\x13"),
		"unknown message":  append(append([]byte(nil), rfbHandshake...), 99),
		"qemu audio":       append(append([]byte(nil), rfbHandshake...), 255, 1, 0, 0),
		"huge clipboard":   append(append([]byte(nil), rfbHandshake...), 6, 0, 0, 0, 0x7f, 0xff, 0xff, 0xff),
	} {
		var filter rfbFilter
		if _, err := filter.feed(stream); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	var filter rfbFilter
	filter.feed(rfbHandshake)
	if _, err := filter.feed([]byte{6, 0, 0, 0, 0, 0x10, 0, 0}); err == nil {
		t.Error("clipboard larger than the pending bound accepted")
	}
}

// Regression for SECURITY-REVIEW.md D2: a view without the input claim cannot
// inject keyboard or pointer input, but still requests framebuffer updates.
func TestDesktopWriteRequiresInputClaim(t *testing.T) {
	manager := &managerConnection{}
	open := func(id string) (*desktopSession, *bytes.Buffer, func()) {
		local, remote := net.Pipe()
		received := &bytes.Buffer{}
		done := make(chan struct{})
		go func() { io.Copy(received, remote); close(done) }()
		return &desktopSession{id: id, manager: manager, conn: local}, received, func() { local.Close(); <-done }
	}
	a, fromA, closeA := open("a")
	b, fromB, closeB := open("b")
	write := func(v *desktopSession, sequence uint64, data []byte) int {
		t.Helper()
		dropped, err := v.write(sequence, data)
		if err != nil {
			t.Fatal(err)
		}
		return dropped
	}
	write(a, 1, rfbHandshake)
	write(b, 1, rfbHandshake)
	if err := manager.claim("a"); err != nil {
		t.Fatal(err)
	}
	if dropped := write(b, 2, bytes.Join([][]byte{rfbKey, rfbUpdate, rfbPointer}, nil)); dropped != 2 {
		t.Fatalf("non-owner dropped %d input messages, want 2", dropped)
	}
	if dropped := write(a, 2, rfbKey); dropped != 0 {
		t.Fatal("owner input dropped")
	}
	manager.release("a")
	if dropped := write(a, 3, rfbPointer); dropped != 1 {
		t.Fatal("input delivered after the claim was released")
	}
	// A terminal view holding the shared box claim also blocks desktop input.
	if err := manager.claim("terminal"); err != nil {
		t.Fatal(err)
	}
	if dropped := write(b, 3, rfbKey); dropped != 1 {
		t.Fatal("desktop input bypassed a terminal claim")
	}
	time.Sleep(10 * time.Millisecond)
	closeA()
	closeB()
	if want := append(append([]byte(nil), rfbHandshake...), rfbKey...); !bytes.Equal(fromA.Bytes(), want) {
		t.Fatalf("owner view delivered %v, want %v", fromA.Bytes(), want)
	}
	if want := append(append([]byte(nil), rfbHandshake...), rfbUpdate...); !bytes.Equal(fromB.Bytes(), want) {
		t.Fatalf("non-owner view delivered %v, want only handshake and update request", fromB.Bytes())
	}
}
