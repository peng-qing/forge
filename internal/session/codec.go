package session

import (
	"encoding/binary"
	"errors"
	"fmt"
	"forge/api/pb"
	"io"

	"github.com/peng-qing/go_tools/common/network"
	"google.golang.org/protobuf/proto"
)

// 协议结构设计 8byte header
// +-------------+-------------+-------------------+
// | magic       | version     | body_length       |
// | uint16      | uint16      | uint32            |
// | 2 字节      | 2 字节      | 4 字节            |
// +-------------+-------------+-------------------+

const (
	// 默认最大包体大小 64 Kb
	defaultMaxBodyBytes = 64 * 1024
	// FrameHeaderSize 固定帧头长度
	FrameHeaderSize = 8
	// FrameMagic 对应 ASCII "FG"
	FrameMagic uint16 = 0x4647
	// FrameVersion 当前通信协议版本
	FrameVersion uint16 = 1
)

var (
	// 检查实现 network.Codec[P] 接口
	_ network.Codec[*pb.Message] = (*Codec)(nil)
)

var (
	ErrEmptyFrameBody     = errors.New("codec: empty frame body")
	ErrFrameBodyTooLarge  = errors.New("codec: frame body too large")
	ErrInvalidFrameMagic  = errors.New("codec: invalid frame magic")
	ErrUnsupportedVersion = errors.New("codec: unsupported frame version")
)

// Codec 实现 Forge 统一消息的分帧和编解码
// Codec 不解析业务 Payload 不校验信息是否可信
type Codec struct {
	maxBodyBytes int              // 最大包体限制
	byteOrder    binary.ByteOrder // 字节序
}

// NewCodec 创建消息编解码器
// maxBodyBytes 表示 Protobuf Message 编码后的最大长度 不包含 FrameHeaderSize
func NewCodec(maxBodyBytes int, isLittleEndian bool) *Codec {
	if maxBodyBytes <= 0 {
		maxBodyBytes = defaultMaxBodyBytes
	}
	var byteOrder binary.ByteOrder = binary.BigEndian
	if isLittleEndian {
		byteOrder = binary.LittleEndian
	}

	return &Codec{maxBodyBytes: maxBodyBytes, byteOrder: byteOrder}
}

// Encode 将一条 Message 编码为完整网络帧
func (c *Codec) Encode(msg *pb.Message) ([]byte, error) {
	if msg == nil || msg.Header == nil {
		return nil, fmt.Errorf("codec: nil message or message header")
	}
	// 序列化
	body, err := proto.Marshal(msg)
	if err != nil {
		return nil, err
	}
	bodyLen := len(body)
	if bodyLen <= 0 {
		return nil, ErrEmptyFrameBody
	}
	if bodyLen > c.maxBodyBytes {
		return nil, ErrFrameBodyTooLarge
	}

	// 包头 8 字节
	frame := make([]byte, FrameHeaderSize+bodyLen)
	c.byteOrder.PutUint16(frame[0:2], FrameMagic)
	c.byteOrder.PutUint16(frame[2:4], FrameVersion)
	c.byteOrder.PutUint32(frame[4:8], uint32(bodyLen))
	// 包体
	copy(frame[FrameHeaderSize:], body)

	return frame, nil
}

// Decode 从 reader 中读取并解码一条完整网络帧
func (c *Codec) Decode(reader io.Reader) (*pb.Message, int, error) {
	if reader == nil {
		return nil, 0, fmt.Errorf("codec: invalid io reader")
	}
	// 先解析包头
	var frameHeader [FrameHeaderSize]byte
	headerBytes, err := io.ReadFull(reader, frameHeader[:])
	if err != nil {
		return nil, headerBytes, err
	}
	magic := c.byteOrder.Uint16(frameHeader[0:2])
	version := c.byteOrder.Uint16(frameHeader[2:4])
	bodyLen := c.byteOrder.Uint32(frameHeader[4:8])
	if magic != FrameMagic {
		return nil, headerBytes, ErrInvalidFrameMagic
	}
	if version != FrameVersion {
		return nil, headerBytes, ErrUnsupportedVersion
	}
	if bodyLen <= 0 {
		return nil, headerBytes, ErrEmptyFrameBody
	}
	if bodyLen > uint32(c.maxBodyBytes) {
		return nil, headerBytes, ErrFrameBodyTooLarge
	}

	// 根据包体长度解析后续
	body := make([]byte, int(bodyLen))
	bodyBytes, err := io.ReadFull(reader, body)
	consumed := bodyBytes + headerBytes
	if err != nil {
		return nil, consumed, err
	}

	msg := &pb.Message{}
	if err := proto.Unmarshal(body, msg); err != nil {
		return nil, consumed, err
	}

	return msg, consumed, nil
}
