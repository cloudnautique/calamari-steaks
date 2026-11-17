package bridge

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func rfbMessage(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
func be16(v uint16) []byte              { return binary.BigEndian.AppendUint16(nil, v) }
func be32(v uint32) []byte              { return binary.BigEndian.AppendUint32(nil, v) }

// Every message noVNC sends, in a stream after a 3.8 None handshake.
func TestRFBInputFilterFramesNoVNCMessages(t *testing.T) {
	handshake := rfbMessage([]byte("RFB 003.008\n"), []byte{1, 1})
	pixelFormat := append([]byte{rfbSetPixelFormat}, make([]byte, 19)...)
	encodings := rfbMessage([]byte{rfbSetEncodings, 0}, be16(3), be32(7), be32(0xFFFFFEC4) /* -316 */, be32(0xFFFFFF21))
	encodingsSent := rfbMessage([]byte{rfbSetEncodings, 0}, be16(2), be32(7), be32(0xFFFFFF21))
	update := []byte{rfbFramebufferUpdateRequest, 1, 0, 0, 0, 0, 0, 10, 0, 10}
	continuous := []byte{rfbEnableContinuousUpdates, 1, 0, 0, 0, 0, 0, 10, 0, 10}
	fence := rfbMessage([]byte{rfbClientFence, 0, 0, 0}, be32(1), []byte{3, 'a', 'b', 'c'})
	key := []byte{rfbKeyEvent, 1, 0, 0, 0, 0, 0, 0x61}
	pointer := []byte{rfbPointerEvent, 1, 0, 5, 0, 6}
	qemuKey := rfbMessage([]byte{rfbQEMU, 0}, be16(1), be32(0x61), be32(30))
	cut := rfbMessage([]byte{rfbClientCutText, 0, 0, 0}, be32(5), []byte("hello"))
	extendedCut := rfbMessage([]byte{rfbClientCutText, 0, 0, 0}, be32(uint32(0x100000000-8)), be32(1<<28 /* provide */ |1), make([]byte, 4))
	clipboardCaps := rfbMessage([]byte{rfbClientCutText, 0, 0, 0}, be32(uint32(0x100000000-8)), be32(0x1F<<24 /* caps, request, peek, notify, provide, as noVNC sends */ |1), be32(1024))
	resize := rfbMessage([]byte{rfbSetDesktopSize, 0}, be16(800), be16(600), []byte{1, 0}, make([]byte, 16))
	xvp := []byte{rfbXvp, 0, 1, 2}
	viewing := rfbMessage(pixelFormat, encodings, update, continuous, fence, clipboardCaps)
	viewingSent := rfbMessage(pixelFormat, encodingsSent, update, continuous, fence, clipboardCaps)
	input := rfbMessage(key, pointer, qemuKey, cut, extendedCut, resize, xvp)
	stream := rfbMessage(handshake, viewing, input, update)

	for _, tc := range []struct {
		name  string
		owned bool
		want  []byte
	}{
		{"controller", true, rfbMessage(handshake, viewingSent, input, update)},
		{"viewer", false, rfbMessage(handshake, viewingSent, update)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Whole batch, and one byte per batch: framing must survive any split.
			for _, step := range []int{len(stream), 1, 7} {
				var f rfbInputFilter
				var got []byte
				for rest := stream; len(rest) > 0; {
					n := min(step, len(rest))
					out, err := f.filter(rest[:n], tc.owned)
					if err != nil {
						t.Fatalf("step %d: %v", step, err)
					}
					got, rest = append(got, out...), rest[n:]
				}
				if !bytes.Equal(got, tc.want) {
					t.Fatalf("step %d forwarded\n%v\nwant\n%v", step, got, tc.want)
				}
			}
		})
	}
}

// Ownership is decided per message: a batch only forwards input while owned.
func TestRFBInputFilterFollowsOwnershipChanges(t *testing.T) {
	var f rfbInputFilter
	key := []byte{rfbKeyEvent, 1, 0, 0, 0, 0, 0, 0x61}
	if _, err := f.filter(append([]byte("RFB 003.008\n"), 1, 1), false); err != nil {
		t.Fatal(err)
	}
	if out, _ := f.filter(key, false); len(out) != 0 {
		t.Fatal("viewer input forwarded")
	}
	if out, _ := f.filter(key, true); !bytes.Equal(out, key) {
		t.Fatal("controller input dropped")
	}
	// A clipboard message split across a release stays whole: its body follows
	// the decision made at its header.
	cut := rfbMessage([]byte{rfbClientCutText, 0, 0, 0}, be32(4), []byte("data"))
	head, _ := f.filter(cut[:9], true)
	tail, _ := f.filter(cut[9:], false)
	if !bytes.Equal(append(head, tail...), cut) {
		t.Fatal("split clipboard message was torn")
	}
}

func TestRFBInputFilterHandshakes(t *testing.T) {
	key := []byte{rfbKeyEvent, 1, 0, 0, 0, 0, 0, 0x61}
	for name, handshake := range map[string][]byte{
		"3.7 None":         append([]byte("RFB 003.007\n"), 1, 1),
		"3.8 VNC auth":     rfbMessage([]byte("RFB 003.008\n"), []byte{2}, bytes.Repeat([]byte{9}, 16), []byte{1}),
		"Apple 3.889 None": append([]byte("RFB 003.889\n"), 1, 1),
	} {
		var f rfbInputFilter
		out, err := f.filter(append(append([]byte(nil), handshake...), key...), false)
		if err != nil || !bytes.Equal(out, handshake) {
			t.Fatalf("%s: forwarded %v, %v", name, out, err)
		}
	}
}

// What the filter cannot frame closes the desktop instead of passing through.
func TestRFBInputFilterFailsClosed(t *testing.T) {
	handshake := append([]byte("RFB 003.008\n"), 1, 1)
	for name, stream := range map[string][]byte{
		"not RFB":                 []byte("GET / HTTP/1.1\r\n"),
		"RFB 4":                   []byte("RFB 004.000\n"),
		"RFB 3.3":                 []byte("RFB 003.003\n"),
		"unknown security type":   append([]byte("RFB 003.008\n"), 19),
		"unknown message":         append(append([]byte(nil), handshake...), 7),
		"unknown QEMU message":    append(append([]byte(nil), handshake...), rfbQEMU, 1),
		"oversized clipboard":     rfbMessage(handshake, []byte{rfbClientCutText, 0, 0, 0}, be32(rfbMaxCutText+1)),
		"oversized SetEncodings":  rfbMessage(handshake, []byte{rfbSetEncodings, 0}, be16(rfbMaxEncoding+1)),
		"input after bad version": rfbMessage([]byte("RFB 003.00x\n"), []byte{rfbKeyEvent}),
	} {
		var f rfbInputFilter
		if _, err := f.filter(stream, true); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}
