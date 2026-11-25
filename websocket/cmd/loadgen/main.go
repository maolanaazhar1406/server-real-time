package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/belajar/server-real-time/internal/protocol"
	"github.com/belajar/server-real-time/internal/transport/websocket"
)

func main() {
	proto := flag.String("proto", "tcp", "transport protocol: tcp | ws | udp")
	addr := flag.String("addr", "127.0.0.1:9000", "target server address (or ws://host:port/ws for ws)")
	conns := flag.Int("conns", 1000, "number of concurrent simulated connections")
	msgsPerConn := flag.Int("msgs", 5, "messages sent per connection")
	concurrency := flag.Int("workers", 64, "dialing worker concurrency")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var connected atomic.Int64
	var failed atomic.Int64
	var sent atomic.Int64

	jobs := make(chan int, *conns)
	for i := 0; i < *conns; i++ {
		jobs <- i
	}
	close(jobs)

	start := time.Now()
	var wg sync.WaitGroup
	for w := 0; w < *concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				if ctx.Err() != nil {
					return
				}
				var err error
				switch *proto {
				case "tcp":
					err = runTCPClient(ctx, *addr, id, *msgsPerConn, &sent)
				case "ws":
					err = runWSClient(ctx, *addr, id, *msgsPerConn, &sent)
				case "udp":
					err = runUDPClient(ctx, *addr, id, *msgsPerConn, &sent)
				default:
					err = fmt.Errorf("unknown protocol %q", *proto)
				}
				if err != nil {
					failed.Add(1)
				} else {
					connected.Add(1)
				}
			}
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)
	fmt.Printf("loadgen completed: proto=%s target=%s ok=%d failed=%d msgs_sent=%d duration=%s\n",
		*proto, *addr, connected.Load(), failed.Load(), sent.Load(), elapsed.Round(time.Millisecond))
}

func runTCPClient(ctx context.Context, addr string, id, msgs int, sent *atomic.Int64) error {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()

	join, _ := (&protocol.Envelope{Type: protocol.TypeJoinRoom, Topic: "loadtest"}).EncodeBinary()
	if _, err := c.Write(protocol.EncodeTCPFrame(join)); err != nil {
		return err
	}

	for i := 0; i < msgs; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		env := &protocol.Envelope{
			Type:      protocol.TypeData,
			Topic:     "loadtest",
			SenderID:  fmt.Sprintf("client-%d", id),
			Payload:   []byte("ping-load"),
			Timestamp: time.Now().UnixMilli(),
		}
		b, _ := env.EncodeBinary()
		if _, err := c.Write(protocol.EncodeTCPFrame(b)); err != nil {
			return err
		}
		sent.Add(1)
	}
	return nil
}

func runWSClient(ctx context.Context, wsURL string, id, msgs int, sent *atomic.Int64) error {
	c, err := websocket.Dial(wsURL, false)
	if err != nil {
		return err
	}
	defer c.Close()

	join, _ := (&protocol.Envelope{Type: protocol.TypeJoinRoom, Topic: "loadtest"}).EncodeBinary()
	if err := c.WriteMessage(true, join); err != nil {
		return err
	}
	for i := 0; i < msgs; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		env := &protocol.Envelope{
			Type:      protocol.TypeData,
			Topic:     "loadtest",
			SenderID:  fmt.Sprintf("ws-client-%d", id),
			Payload:   []byte("ping-load"),
			Timestamp: time.Now().UnixMilli(),
		}
		b, _ := env.EncodeBinary()
		if err := c.WriteMessage(true, b); err != nil {
			return err
		}
		sent.Add(1)
	}
	return nil
}

func runUDPClient(ctx context.Context, addr string, id, msgs int, sent *atomic.Int64) error {
	c, err := net.DialTimeout("udp", addr, 5*time.Second)
	if err != nil {
		return err
	}
	defer c.Close()

	hs := protocol.EncodeUDPDatagram(&protocol.UDPDatagram{
		Type:   protocol.UDPHandshake,
		SeqNum: 1,
	})
	if _, err := c.Write(hs); err != nil {
		return err
	}
	buf := make([]byte, 2048)
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := c.Read(buf)
	if err != nil {
		return err
	}
	ack, err := protocol.DecodeUDPDatagram(buf[:n])
	if err != nil {
		return err
	}

	for i := 0; i < msgs; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		env := &protocol.Envelope{
			Type:     protocol.TypeData,
			Topic:    "loadtest",
			SenderID: fmt.Sprintf("udp-client-%d", id),
			Payload:  []byte("udp-ping"),
		}
		rawEnv, _ := env.EncodeBinary()
		pkt := protocol.EncodeUDPDatagram(&protocol.UDPDatagram{
			Type:         protocol.UDPData,
			SessionToken: ack.SessionToken,
			SeqNum:       uint64(i + 2),
			Payload:      rawEnv,
		})
		if _, err := c.Write(pkt); err != nil {
			return err
		}
		sent.Add(1)
	}
	return nil
}
