package session

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"forge/api/pb"

	"google.golang.org/protobuf/proto"
)

func TestNewCodecDefaults(t *testing.T) {
	codec := NewCodec(0, false)
	if codec.maxBodyBytes != defaultMaxBodyBytes {
		t.Fatalf("maxBodyBytes = %d, want %d", codec.maxBodyBytes, defaultMaxBodyBytes)
	}
	if codec.byteOrder != binary.BigEndian {
		t.Fatalf("byteOrder = %T, want big endian", codec.byteOrder)
	}

	littleEndianCodec := NewCodec(-1, true)
	if littleEndianCodec.maxBodyBytes != defaultMaxBodyBytes {
		t.Fatalf("little endian maxBodyBytes = %d, want %d", littleEndianCodec.maxBodyBytes, defaultMaxBodyBytes)
	}
	if littleEndianCodec.byteOrder != binary.LittleEndian {
		t.Fatalf("byteOrder = %T, want little endian", littleEndianCodec.byteOrder)
	}
}

func TestCodecRoundTrip(t *testing.T) {
	tests := []struct {
		name         string
		littleEndian bool
		byteOrder    binary.ByteOrder
	}{
		{name: "big endian", byteOrder: binary.BigEndian},
		{name: "little endian", littleEndian: true, byteOrder: binary.LittleEndian},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codec := NewCodec(0, tt.littleEndian)
			want := newTestMessage([]byte("hello forge"))

			frame, err := codec.Encode(want)
			if err != nil {
				t.Fatalf("Encode() error = %v", err)
			}
			if len(frame) <= FrameHeaderSize {
				t.Fatalf("frame length = %d", len(frame))
			}
			if got := tt.byteOrder.Uint16(frame[0:2]); got != FrameMagic {
				t.Fatalf("magic = %#x, want %#x", got, FrameMagic)
			}
			if got := tt.byteOrder.Uint16(frame[2:4]); got != FrameVersion {
				t.Fatalf("version = %d, want %d", got, FrameVersion)
			}
			if got := tt.byteOrder.Uint32(frame[4:8]); got != uint32(len(frame)-FrameHeaderSize) {
				t.Fatalf("body length = %d, want %d", got, len(frame)-FrameHeaderSize)
			}

			got, consumed, err := codec.Decode(bytes.NewReader(frame))
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if consumed != len(frame) {
				t.Fatalf("Decode() consumed = %d, want %d", consumed, len(frame))
			}
			if !proto.Equal(got, want) {
				t.Fatalf("Decode() message = %v, want %v", got, want)
			}
		})
	}
}

func TestCodecDecodeConsecutiveFrames(t *testing.T) {
	codec := NewCodec(0, false)
	first := newTestMessage([]byte("first"))
	second := newTestMessage([]byte("second"))

	firstFrame, err := codec.Encode(first)
	if err != nil {
		t.Fatalf("encode first frame: %v", err)
	}
	secondFrame, err := codec.Encode(second)
	if err != nil {
		t.Fatalf("encode second frame: %v", err)
	}

	reader := bytes.NewReader(append(firstFrame, secondFrame...))
	for index, want := range []*pb.Message{first, second} {
		got, consumed, err := codec.Decode(reader)
		if err != nil {
			t.Fatalf("Decode(%d) error = %v", index, err)
		}
		if consumed != FrameHeaderSize+proto.Size(want) {
			t.Fatalf("Decode(%d) consumed = %d", index, consumed)
		}
		if !proto.Equal(got, want) {
			t.Fatalf("Decode(%d) message = %v, want %v", index, got, want)
		}
	}
	if reader.Len() != 0 {
		t.Fatalf("reader has %d trailing bytes", reader.Len())
	}
}

func TestCodecDecodeFragmentedFrame(t *testing.T) {
	codec := NewCodec(0, false)
	want := newTestMessage(bytes.Repeat([]byte{0x7a}, 128))
	frame, err := codec.Encode(want)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	reader := &chunkReader{data: frame, maxChunk: 3}
	got, consumed, err := codec.Decode(reader)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if consumed != len(frame) {
		t.Fatalf("Decode() consumed = %d, want %d", consumed, len(frame))
	}
	if !proto.Equal(got, want) {
		t.Fatalf("Decode() message = %v, want %v", got, want)
	}
}

func TestCodecBodySizeBoundary(t *testing.T) {
	msg := newTestMessage(bytes.Repeat([]byte{0x5a}, 32))
	body, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal test message: %v", err)
	}

	codec := NewCodec(len(body), false)
	if _, err := codec.Encode(msg); err != nil {
		t.Fatalf("Encode() at limit error = %v", err)
	}

	tooSmallCodec := NewCodec(len(body)-1, false)
	if _, err := tooSmallCodec.Encode(msg); !errors.Is(err, ErrFrameBodyTooLarge) {
		t.Fatalf("Encode() error = %v, want ErrFrameBodyTooLarge", err)
	}
}

func TestCodecEncodeRejectsInvalidMessage(t *testing.T) {
	codec := NewCodec(0, false)

	if _, err := codec.Encode(nil); err == nil {
		t.Fatal("Encode(nil) error = nil")
	}
	if _, err := codec.Encode(&pb.Message{}); err == nil {
		t.Fatal("Encode(message without header) error = nil")
	}
}

func TestCodecDecodeRejectsInvalidHeader(t *testing.T) {
	tests := []struct {
		name      string
		codec     *Codec
		magic     uint16
		version   uint16
		bodyLen   uint32
		wantError error
	}{
		{
			name:      "invalid magic",
			codec:     NewCodec(0, false),
			magic:     FrameMagic + 1,
			version:   FrameVersion,
			bodyLen:   1,
			wantError: ErrInvalidFrameMagic,
		},
		{
			name:      "unsupported version",
			codec:     NewCodec(0, false),
			magic:     FrameMagic,
			version:   FrameVersion + 1,
			bodyLen:   1,
			wantError: ErrUnsupportedVersion,
		},
		{
			name:      "empty body",
			codec:     NewCodec(0, false),
			magic:     FrameMagic,
			version:   FrameVersion,
			wantError: ErrEmptyFrameBody,
		},
		{
			name:      "body too large",
			codec:     NewCodec(16, false),
			magic:     FrameMagic,
			version:   FrameVersion,
			bodyLen:   17,
			wantError: ErrFrameBodyTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frame := makeFrameHeader(tt.codec.byteOrder, tt.magic, tt.version, tt.bodyLen)
			_, consumed, err := tt.codec.Decode(bytes.NewReader(frame))
			if !errors.Is(err, tt.wantError) {
				t.Fatalf("Decode() error = %v, want %v", err, tt.wantError)
			}
			if consumed != FrameHeaderSize {
				t.Fatalf("Decode() consumed = %d, want %d", consumed, FrameHeaderSize)
			}
		})
	}
}

func TestCodecDecodeReportsConsumedBytesOnTruncatedFrame(t *testing.T) {
	codec := NewCodec(0, false)

	_, consumed, err := codec.Decode(bytes.NewReader([]byte{0x46, 0x47, 0x00}))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated header error = %v, want io.ErrUnexpectedEOF", err)
	}
	if consumed != 3 {
		t.Fatalf("truncated header consumed = %d, want 3", consumed)
	}

	header := makeFrameHeader(binary.BigEndian, FrameMagic, FrameVersion, 5)
	frame := append(header, 0x01, 0x02)
	_, consumed, err = codec.Decode(bytes.NewReader(frame))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated body error = %v, want io.ErrUnexpectedEOF", err)
	}
	if consumed != FrameHeaderSize+2 {
		t.Fatalf("truncated body consumed = %d, want %d", consumed, FrameHeaderSize+2)
	}
}

func TestCodecDecodeRejectsMalformedProtobuf(t *testing.T) {
	codec := NewCodec(0, false)
	header := makeFrameHeader(binary.BigEndian, FrameMagic, FrameVersion, 1)
	frame := append(header, 0x80)

	_, consumed, err := codec.Decode(bytes.NewReader(frame))
	if err == nil {
		t.Fatal("Decode() malformed protobuf error = nil")
	}
	if consumed != len(frame) {
		t.Fatalf("Decode() consumed = %d, want %d", consumed, len(frame))
	}
}

func TestCodecDecodeRejectsNilReader(t *testing.T) {
	codec := NewCodec(0, false)
	if _, consumed, err := codec.Decode(nil); err == nil || consumed != 0 {
		t.Fatalf("Decode(nil) = consumed %d, error %v", consumed, err)
	}
}

func FuzzCodecDecode(f *testing.F) {
	codec := NewCodec(0, false)
	frame, err := codec.Encode(newTestMessage([]byte("seed")))
	if err != nil {
		f.Fatalf("encode seed frame: %v", err)
	}
	f.Add(frame)
	f.Add([]byte{})
	f.Add([]byte{0x46, 0x47, 0x00})

	f.Fuzz(func(t *testing.T, data []byte) {
		_, _, _ = codec.Decode(bytes.NewReader(data))
	})
}

func newTestMessage(payload []byte) *pb.Message {
	return &pb.Message{
		Header: &pb.MessageHeader{
			CmdId:     pb.CommandID_COMMAND_ID_C2L_LOGIN,
			Kind:      pb.MessageKind_MESSAGEKIND_REQUEST,
			RequestId: 1,
			Uid:       1001,
			SessionId: 2001,
		},
		Payload: payload,
	}
}

func makeFrameHeader(
	byteOrder binary.ByteOrder,
	magic uint16,
	version uint16,
	bodyLength uint32,
) []byte {
	header := make([]byte, FrameHeaderSize)
	byteOrder.PutUint16(header[0:2], magic)
	byteOrder.PutUint16(header[2:4], version)
	byteOrder.PutUint32(header[4:8], bodyLength)
	return header
}

type chunkReader struct {
	data     []byte
	maxChunk int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	if len(p) > r.maxChunk {
		p = p[:r.maxChunk]
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}
