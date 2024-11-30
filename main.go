package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/urfave/cli/v2"
	"github.com/vmihailenco/msgpack/v5"
	"golang.design/x/clipboard"
)

const (
	serviceName        = "omniclip"
	defaultServicePort = 49435
	defaultStatusPort  = 49436
)

type entry struct {
	Value  string
	Source peer.ID
}

type statusResponse struct {
	Node  string
	Topic string
	Peers []string
}

type discoverer struct {
	h host.Host
}

func main() {
	app := cli.App{
		Name: "omniClip",
		Commands: []*cli.Command{
			{
				Name:    "status",
				Aliases: []string{"s"},
				Action: func(c *cli.Context) error {
					return getStatus(defaultStatusPort)
				},
			},
			{
				Name: "start",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "topic",
						Value:    "",
						Usage:    "Topic to which nodes will subscribe and listen for clipboard events",
						Required: true,
					},
					&cli.IntFlag{
						Name:  "port",
						Value: defaultServicePort,
						Usage: "Port to use to connect to",
					},
					&cli.IntFlag{
						Name:  "status",
						Value: defaultStatusPort,
						Usage: "Port on which to run the status server",
					},
				},
				Action: func(c *cli.Context) error {
					return startNode(c.Context, c.Int("port"), c.Int("status"), c.String("topic"))
				},
			},
		},
		ErrWriter: io.Discard,
	}

	err := app.Run(os.Args)
	if err != nil {
		os.Exit(1)
	}
}

func (n *discoverer) HandlePeerFound(p peer.AddrInfo) {
	if p.ID != n.h.ID() {
		err := n.h.Connect(context.Background(), p)
		if err != nil {
			log.Printf("error connecting to peer %s: %s\n", p.ID, err)
		}
	}
}

func startNode(ctx context.Context, p2pPort, statusPort int, topicName string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	h, err := libp2p.New(libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", p2pPort)))
	if err != nil {
		return fmt.Errorf("error creating libp2p host: %w", err)
	}

	err = clipboard.Init()
	if err != nil {
		return fmt.Errorf("error initializing clipboard: %w", err)
	}

	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		return fmt.Errorf("error creating pubsub service: %w", err)
	}

	topic, err := ps.Join(topicName)
	if err != nil {
		return fmt.Errorf("error joining pubsub topic: %w", err)
	}

	eventsCh := clipboard.Watch(ctx, clipboard.FmtText)

	subscription, err := topic.Subscribe()
	if err != nil {
		return fmt.Errorf("error subscribing to pubsub topic: %w", err)
	}

	errChan := make(chan error)
	server := initServer(statusPort, h, topic)
	go func() {
		if err := server.ListenAndServe(); err != nil {
			errChan <- err
		}
	}()

	go func(ho host.Host) {
		if dnsErr := setupDNSDiscovery(ho); dnsErr != nil {
			errChan <- dnsErr
		}
	}(h)

	go watchClipboard(ctx, topic, eventsCh, h.ID())

	go receiveMessages(ctx, subscription, h.ID())

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-stop:
		timeoutCtx, svCancel := context.WithTimeout(ctx, 3*time.Second)
		defer svCancel()
		svErr := server.Shutdown(timeoutCtx)
		return fmt.Errorf("server shutdown timed out: %w", svErr)
	case svErr := <-errChan:
		return svErr
	}
}

func initServer(port int, h host.Host, tp *pubsub.Topic) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		peerIds := tp.ListPeers()
		peers := make([]string, 0, len(peerIds))
		for _, id := range peerIds {
			peers = append(peers, id.String())
		}
		status := statusResponse{
			Node:  h.ID().String(),
			Topic: tp.String(),
			Peers: peers,
		}
		err := json.NewEncoder(w).Encode(status)
		if err != nil {
			log.Println(err)
		}
	})

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 1 * time.Second,
	}

	return server
}

func getStatus(port int) error {
	client := http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://0.0.0.0:%d/status", port))
	if err != nil {
		fmt.Println("node status: 🛑\n\nis omniClip running?")
		return fmt.Errorf("could not connect to server: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		fmt.Println("node status: 🛑\n\nresponse status not ok")
		return errors.New("node status not ok")
	}
	status := statusResponse{}
	err = json.NewDecoder(resp.Body).Decode(&status)
	if err != nil {
		fmt.Println("node status: 🛑\n\nsomething went wrong")
		return fmt.Errorf("failed to decode response body: %w", err)
	}

	fmt.Println("node status: ✅ ")
	fmt.Printf("topic: 📝%s\n", status.Topic)
	fmt.Println("Peers:")
	for _, p := range status.Peers {
		fmt.Printf("  ✅ %s\n", p)
	}
	return nil
}

func setupDNSDiscovery(h host.Host) error {
	s := mdns.NewMdnsService(h, serviceName, &discoverer{h: h})
	err := s.Start()
	if err != nil {
		return fmt.Errorf("failed to start mdns service: %w", err)
	}
	return nil
}

func watchClipboard(ctx context.Context, topic *pubsub.Topic, watch <-chan []byte, hostID peer.ID) {
	for msg := range watch {
		e := &entry{Value: string(msg), Source: hostID}
		data, err := msgpack.Marshal(e)
		if err != nil {
			log.Println(err)
		}

		if err := topic.Publish(ctx, data); err != nil {
			log.Println(err)
		}
	}
}

func receiveMessages(ctx context.Context, sub *pubsub.Subscription, hostID peer.ID) {
	for {
		m, err := sub.Next(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			log.Println(err)
			continue
		}

		e := &entry{}
		err = msgpack.Unmarshal(m.Data, e)
		if err != nil {
			log.Println(err)
			continue
		}
		if e.Source != hostID {
			clipboard.Write(clipboard.FmtText, []byte(e.Value))
		}
	}
}
