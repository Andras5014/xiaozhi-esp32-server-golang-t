package websocket

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
	"xiaozhi-esp32-server-golang/internal/app/server/types"
	log "xiaozhi-esp32-server-golang/logger"

	"github.com/gorilla/websocket"
)

const (
	websocketPingInterval     = 30 * time.Second
	websocketPingWriteTimeout = 5 * time.Second
	websocketPongTimeout      = 90 * time.Second
)

// WebSocketConn 实现 types.IConn 接口，适配 WebSocket 连接
type WebSocketConn struct {
	ctx    context.Context
	cancel context.CancelFunc

	onCloseCbList []func(deviceId string)

	conn     *websocket.Conn
	deviceID string

	isMqttUdpBridge bool
	recvCmdChan     chan []byte
	recvAudioChan   chan []byte

	createdAt         time.Time
	localAddr         string
	remoteAddr        string
	lastReadUnixNano  int64
	lastWriteUnixNano int64
	lastPongUnixNano  int64
	lastCmdUnixNano   int64
	lastAudioUnixNano int64

	closed          bool
	closeNotifyOnce sync.Once
	sync.RWMutex
}

// NewWebSocketConn 创建一个新的 WebSocketConn 实例
func NewWebSocketConn(conn *websocket.Conn, deviceID string, isMqttUdpBridge bool) *WebSocketConn {
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	instance := &WebSocketConn{
		ctx:             ctx,
		cancel:          cancel,
		conn:            conn,
		deviceID:        deviceID,
		isMqttUdpBridge: isMqttUdpBridge,
		recvCmdChan:     make(chan []byte, 100),
		recvAudioChan:   make(chan []byte, 100),
		createdAt:       now,
	}
	if conn != nil {
		if addr := conn.LocalAddr(); addr != nil {
			instance.localAddr = addr.String()
		}
		if addr := conn.RemoteAddr(); addr != nil {
			instance.remoteAddr = addr.String()
		}
	}
	instance.markRead(now)
	instance.markWrite(now)
	instance.markPong(now)

	// 设置pong处理器
	conn.SetPongHandler(func(appData string) error {
		instance.markPong(time.Now())
		log.Debugf("收到pong消息，设备ID: %s, remote=%s", deviceID, instance.remoteAddr)
		return nil
	})

	// 启动心跳检测goroutine
	go func() {
		ticker := time.NewTicker(websocketPingInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if instance.sinceLastPong() > websocketPongTimeout {
					_ = instance.closeWithReason("pong_timeout",
						fmt.Errorf("last pong exceeded timeout: idle=%s timeout=%s", instance.sinceLastPong(), websocketPongTimeout),
						true)
					return
				}
				pingAt := time.Now()
				if err := instance.conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(websocketPingWriteTimeout)); err != nil {
					// 心跳失败时走完整关闭流程，避免只通知上层但连接资源未清理。
					_ = instance.closeWithReason("ping_failed", err, true)
					return
				}
				instance.markWrite(pingAt)
				log.Debugf("发送ping消息成功，设备ID: %s, remote=%s, 距上次pong=%s",
					deviceID, instance.remoteAddr, instance.sinceLastPong())
			case <-instance.ctx.Done():
				return
			}
		}
	}()

	go func() {
		for {
			select {
			case <-instance.ctx.Done():
				return
			default:
				msgType, audio, err := instance.conn.ReadMessage()
				if err != nil {
					_ = instance.closeWithReason("read_failed", err, true)
					return
				}
				readAt := time.Now()
				instance.markRead(readAt)

				if msgType == websocket.TextMessage {
					instance.markCmd(readAt)
					select {
					case instance.recvCmdChan <- audio:
					default:
						log.Errorf("recv cmd channel is full, device=%s, remote=%s, cmd_bytes=%d, 连接信息: %s",
							deviceID, instance.remoteAddr, len(audio), instance.connectionSnapshot())
					}
				} else if msgType == websocket.BinaryMessage {
					if instance.isMqttUdpBridge {
						audio = instance.tryUnpackUdpBridgeAudioPacket(audio)
					}
					instance.markAudio(readAt)
					select {
					case instance.recvAudioChan <- audio:
					default:
						log.Errorf("recv audio channel is full, device=%s, remote=%s, audio_bytes=%d, 连接信息: %s",
							deviceID, instance.remoteAddr, len(audio), instance.connectionSnapshot())
					}
				}
			}
		}
	}()

	return instance
}

// 适配mqtt udp bridge的数据格式
// 前8个字节为0, 12-16字节为音频数据长度, 16字节后为音频数据
func (c *WebSocketConn) tryUnpackUdpBridgeAudioPacket(buffer []byte) []byte {
	if len(buffer) < 16 {
		return buffer
	}
	// 检查前8字节是否全为0
	for i := 0; i < 8; i++ {
		if buffer[i] != 0 {
			return buffer
		}
	}
	dataLen := binary.BigEndian.Uint32(buffer[12:16])
	if int(dataLen) != len(buffer)-16 {
		return buffer
	}
	audioData := buffer[16:]
	return audioData
}

func (c *WebSocketConn) packUdpBridgeAudioPacket(buffer []byte) []byte {
	header := make([]byte, 16)
	// 前8字节全为0，已初始化
	// 9~12字节写入当前时间戳（秒）
	timestamp := uint32(time.Now().Unix())
	binary.BigEndian.PutUint32(header[8:12], timestamp)
	// 13~16字节写入音频长度
	binary.BigEndian.PutUint32(header[12:16], uint32(len(buffer)))
	// 拼接header和音频数据
	return append(header, buffer...)
}

func (w *WebSocketConn) SendCmd(msg []byte) error {
	w.Lock()
	defer w.Unlock()

	if w.closed {
		return errors.New("connection is closed")
	}

	log.Debugf("send cmd: %s", string(msg))

	err := w.conn.WriteMessage(websocket.TextMessage, msg)
	if err != nil {
		log.Errorf("send cmd error, device=%s, remote=%s, bytes=%d, err=%s, 连接信息: %s",
			w.deviceID, w.remoteAddr, len(msg), describeWSError(err), w.connectionSnapshot())
		return err
	}
	w.markWrite(time.Now())
	return nil
}

func (w *WebSocketConn) SendAudio(audio []byte) error {
	w.Lock()
	defer w.Unlock()

	if w.closed {
		return errors.New("connection is closed")
	}

	if w.isMqttUdpBridge {
		audio = w.packUdpBridgeAudioPacket(audio)
	}
	err := w.conn.WriteMessage(websocket.BinaryMessage, audio)
	if err != nil {
		log.Errorf("send audio error, device=%s, remote=%s, bytes=%d, err=%s, 连接信息: %s",
			w.deviceID, w.remoteAddr, len(audio), describeWSError(err), w.connectionSnapshot())
		return err
	}
	w.markWrite(time.Now())
	return nil
}

func (w *WebSocketConn) RecvCmd(ctx context.Context, timeout int) ([]byte, error) {
	for {
		select {
		case <-ctx.Done():
			log.Debugf("recv cmd context done")
			return nil, ctx.Err()
		case msg, ok := <-w.recvCmdChan:
			if !ok {
				return nil, errors.New("connection is closed")
			}
			return msg, nil
		case <-time.After(time.Duration(timeout) * time.Second):
			return nil, errors.New("timeout")
		}
	}
}

func (w *WebSocketConn) RecvAudio(ctx context.Context, timeout int) ([]byte, error) {
	for {
		select {
		case <-ctx.Done():
			log.Debugf("recv audio context done")
			return nil, ctx.Err()
		case audio, ok := <-w.recvAudioChan:
			if !ok {
				return nil, errors.New("connection is closed")
			}
			return audio, nil
		case <-time.After(time.Duration(timeout) * time.Second):
			return nil, errors.New("timeout")
		}
	}
}

func (w *WebSocketConn) Close() error {
	return w.closeWithReason("", nil, false)
}

func (w *WebSocketConn) closeWithReason(reason string, err error, notify bool) error {
	w.Lock()
	if w.closed {
		w.Unlock()
		return nil // Already closed
	}
	w.closed = true
	conn := w.conn
	recvCmdChan := w.recvCmdChan
	recvAudioChan := w.recvAudioChan
	snapshot := w.connectionSnapshot()
	deviceID := w.deviceID
	remoteAddr := w.remoteAddr
	w.Unlock()

	if notify {
		event, summary := classifyCloseEvent(reason, err)
		log.Warnf("WebSocket连接关闭, device=%s, event=%s, summary=%s, reason=%s, remote=%s, err=%s, 连接信息: %s",
			deviceID, event, summary, reason, remoteAddr, describeWSError(err), snapshot)
	} else {
		log.Infof("关闭WebSocket连接, device=%s, remote=%s, 连接信息: %s",
			deviceID, remoteAddr, snapshot)
	}

	w.cancel()
	if conn != nil {
		_ = conn.Close()
	}
	close(recvCmdChan)
	close(recvAudioChan)

	if notify {
		w.notifyClosed(reason, err)
	}
	return nil
}

func (w *WebSocketConn) OnClose(cb func(deviceId string)) {
	w.onCloseCbList = append(w.onCloseCbList, cb)
}

func (w *WebSocketConn) GetDeviceID() string {
	return w.deviceID
}

func (w *WebSocketConn) GetTransportType() string {
	return types.TransportTypeWebsocket
}

func (w *WebSocketConn) GetData(key string) (interface{}, error) {
	return nil, errors.New("not implemented")
}

func (w *WebSocketConn) CloseAudioChannel() error {
	return nil
}

func (w *WebSocketConn) notifyClosed(reason string, err error) {
	w.closeNotifyOnce.Do(func() {
		for _, cb := range w.onCloseCbList {
			cb(w.deviceID)
		}
	})
}

func (w *WebSocketConn) markRead(t time.Time) {
	atomic.StoreInt64(&w.lastReadUnixNano, t.UnixNano())
}

func (w *WebSocketConn) markWrite(t time.Time) {
	atomic.StoreInt64(&w.lastWriteUnixNano, t.UnixNano())
}

func (w *WebSocketConn) markPong(t time.Time) {
	atomic.StoreInt64(&w.lastPongUnixNano, t.UnixNano())
}

func (w *WebSocketConn) markCmd(t time.Time) {
	atomic.StoreInt64(&w.lastCmdUnixNano, t.UnixNano())
}

func (w *WebSocketConn) markAudio(t time.Time) {
	atomic.StoreInt64(&w.lastAudioUnixNano, t.UnixNano())
}

func (w *WebSocketConn) connectionSnapshot() string {
	now := time.Now()
	return fmt.Sprintf(
		"local=%s remote=%s bridge=%t online_for=%s idle_read=%s idle_write=%s idle_pong=%s idle_cmd=%s idle_audio=%s",
		emptyAsNA(w.localAddr),
		emptyAsNA(w.remoteAddr),
		w.isMqttUdpBridge,
		now.Sub(w.createdAt).Round(time.Millisecond),
		durationSinceUnixNano(atomic.LoadInt64(&w.lastReadUnixNano), now),
		durationSinceUnixNano(atomic.LoadInt64(&w.lastWriteUnixNano), now),
		durationSinceUnixNano(atomic.LoadInt64(&w.lastPongUnixNano), now),
		durationSinceUnixNano(atomic.LoadInt64(&w.lastCmdUnixNano), now),
		durationSinceUnixNano(atomic.LoadInt64(&w.lastAudioUnixNano), now),
	)
}

func (w *WebSocketConn) sinceLastPong() time.Duration {
	lastPong := atomic.LoadInt64(&w.lastPongUnixNano)
	if lastPong == 0 {
		return 0
	}
	return time.Since(time.Unix(0, lastPong)).Round(time.Millisecond)
}

func durationSinceUnixNano(unixNano int64, now time.Time) string {
	if unixNano == 0 {
		return "n/a"
	}
	return now.Sub(time.Unix(0, unixNano)).Round(time.Millisecond).String()
}

func emptyAsNA(v string) string {
	if v == "" {
		return "n/a"
	}
	return v
}

func describeWSError(err error) string {
	if err == nil {
		return "nil"
	}

	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return fmt.Sprintf("websocket_close code=%d text=%q raw=%v", closeErr.Code, closeErr.Text, err)
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return fmt.Sprintf("network_error timeout=%t temporary=%t raw=%v", netErr.Timeout(), netErr.Temporary(), err)
	}

	switch {
	case errors.Is(err, io.ErrUnexpectedEOF):
		return fmt.Sprintf("unexpected_eof raw=%v", err)
	case errors.Is(err, io.EOF):
		return fmt.Sprintf("eof raw=%v", err)
	default:
		return err.Error()
	}
}

func classifyCloseEvent(reason string, err error) (event string, summary string) {
	switch reason {
	case "pong_timeout":
		return "server_heartbeat_timeout_kick", fmt.Sprintf("服务端心跳超时，主动断开连接（超时阈值=%s）", websocketPongTimeout)
	case "ping_failed":
		return "server_heartbeat_ping_failed", "服务端发送心跳失败，连接已失效，主动关闭连接"
	case "read_failed":
		switch {
		case isNormalClientDisconnect(err):
			return "client_normal_disconnect", "客户端正常关闭连接"
		case isAbnormalClientDisconnect(err):
			return "client_abnormal_disconnect", "客户端异常断开或链路中断"
		default:
			return "client_read_failed", "读取客户端消息失败，关闭连接"
		}
	default:
		return "connection_closed", "连接已关闭"
	}
}

func isNormalClientDisconnect(err error) bool {
	return websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway)
}

func isAbnormalClientDisconnect(err error) bool {
	if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		return true
	}

	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return closeErr.Code == websocket.CloseAbnormalClosure
	}

	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}
