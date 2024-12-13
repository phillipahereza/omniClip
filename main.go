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

// entry represents a clipboard entry with its value and source peer ID.
type entry struct {
	Value  string
	Source peer.ID
}

type statusResponse struct {
	Node  string
	Topic string
	Peers []string
}

// discoverer implements the mdns.Notifee interface for peer discovery.
type discoverer struct {
	h host.Host
}

func main() {
	app := &cli.App{
		Name:        "omniClip",
		Usage:       "A cross-platform clipboard sharing tool.",
		Description: "Share your clipboard seamlessly across devices.",
		Commands: []*cli.Command{
			{
				Name:    "status",
				Aliases: []string{"s"},
				Usage:   "Check the status of the omniclip node.",
				Action: func(c *cli.Context) error {
					return getStatus(defaultStatusPort)
				},
			},
			{
				Name:  "start",
				Usage: "Start the omniclip node.",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "topic",
						Aliases:  []string{"t"},
						Value:    "",
						Usage:    "Topic to subscribe to for clipboard events.",
						Required: true,
					},
					&cli.IntFlag{
						Name:    "port",
						Aliases: []string{"p"},
						Value:   defaultServicePort,
						Usage:   "Port for p2p communication.",
					},
					&cli.IntFlag{
						Name:    "status",
						Aliases: []string{"st"},
						Value:   defaultStatusPort,
						Usage:   "Port for the status server.",
					},
				},
				Action: func(c *cli.Context) error {
					return startNode(c.Context, c.Int("port"), c.Int("status"), c.String("topic"))
				},
			},
		},
		ErrWriter: io.Discard,

		Version: "1.0.0",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "version",
				Aliases: []string{"v"},
				Usage:   "Print the version and exit.",
			},
		},
		Before: func(c *cli.Context) error {
			if c.Bool("version") {
				fmt.Println(c.App.Version)
				os.Exit(0)
			}
			return nil
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}

// getStatus retrieves and prints the status of the omniclip node.
func getStatus(port int) error {
	client := http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://0.0.0.0:%d/status", port))
	if err != nil {
		fmt.Println("node status: 🛑\n\nis omniClip running?")
		return fmt.Errorf("connecting to status server: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		fmt.Println("node status: 🛑\n\ninvalid status response")
		return fmt.Errorf("status server returned %s", resp.Status)
	}
	status := statusResponse{}
	err = json.NewDecoder(resp.Body).Decode(&status)
	if err != nil {
		fmt.Println("node status: 🛑\n\nfailed to decode response")
		return fmt.Errorf("decoding response status: %w", err)
	}

	fmt.Println("node status: ✅ ")
	fmt.Printf("topic: 📝%s\n", status.Topic)
	fmt.Println("Peers:")
	for _, p := range status.Peers {
		fmt.Printf("  ✅ %s\n", p)
	}
	return nil
}

func initServer(port int, h host.Host, tp *pubsub.Topic) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		peerIds := tp.ListPeers()
		peers := make([]string, len(peerIds))
		for i, id := range peerIds {
			peers[i] = id.String()
		}
		status := statusResponse{
			Node:  h.ID().String(),
			Topic: tp.String(),
			Peers: peers,
		}
		if err := json.NewEncoder(w).Encode(status); err != nil {
			log.Println("Error encoding status response:", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	})

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 1 * time.Second,
	}

	return server
}

func startNode(ctx context.Context, p2pPort, statusPort int, topicName string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Create libp2p host
	h, err := libp2p.New(libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", p2pPort)))
	if err != nil {
		return fmt.Errorf("error creating libp2p host: %w", err)
	}

	if err := clipboard.Init(); err != nil {
		return fmt.Errorf("error initializing clipboard: %w", err)
	}

	// Setup PubSub
	ps, err := pubsub.NewGossipSub(ctx, h)
	if err != nil {
		return fmt.Errorf("error creating pubsub service: %w", err)
	}

	topic, err := ps.Join(topicName)
	if err != nil {
		return fmt.Errorf("error joining pubsub topic: %w", err)
	}

	subscription, err := topic.Subscribe()
	if err != nil {
		return fmt.Errorf("error subscribing to pubsub topic: %w", err)
	}

	// Start status server
	server := initServer(statusPort, h, topic)
	errChan := make(chan error)

	go func() {
		errChan <- server.ListenAndServe()
	}()

	// Start peer discovery
	go func() {
		errChan <- setupDNSDiscovery(h)
	}()

	// Start Clipboard Watcher and Message Receiver
	go watchClipboard(ctx, topic, clipboard.Watch(ctx, clipboard.FmtText), h.ID())

	go receiveMessages(ctx, subscription, h.ID())

	// Handle graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-stop:
		timeoutCtx, svCancel := context.WithTimeout(ctx, 3*time.Second)
		defer svCancel()
		if err := server.Shutdown(timeoutCtx); err != nil {
			return fmt.Errorf("server shutdown: %w", err)
		}
		return nil

	case svErr := <-errChan:
		return svErr
	}
}

// setupDNSDiscovery sets up mDNS discovery for the libp2p host.
func setupDNSDiscovery(h host.Host) error {
	s := mdns.NewMdnsService(h, serviceName, &discoverer{h: h})
	if err := s.Start(); err != nil {
		return fmt.Errorf("failed to start mdns service: %w", err)
	}
	return nil
}

// HandlePeerFound is called when a new peer is discovered via mDNS.
// It attempts to connect to the peer if it's not the host itself.
func (n *discoverer) HandlePeerFound(pi peer.AddrInfo) {
	if pi.ID == n.h.ID() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	log.Printf("Discovered peer: %s\n", pi.ID.ShortString())

	err := n.h.Connect(ctx, pi)
	if err != nil {
		log.Printf("error connecting to peer %s: %s\n", pi.ID.ShortString(), err)
	}
}

// watchClipboard monitors the clipboard for changes and publishes new entries to the pubsub topic.
func watchClipboard(ctx context.Context, topic *pubsub.Topic, clipboardCh <-chan []byte, id peer.ID) {
	for msg := range clipboardCh {
		e := &entry{Value: string(msg), Source: id}
		data, err := msgpack.Marshal(e)
		if err != nil {
			log.Println("Error marshalling clipboard entry", err)
			continue
		}

		// Publish the serialized entry to the pubsub topic
		if err := topic.Publish(ctx, data); err != nil {
			log.Println(err)
		}
	}
}

// receiveMessages receives messages from the pubsub topic, unmarshals them,
// and updates the local clipboard if the message originated from a different peer.
func receiveMessages(ctx context.Context, sub *pubsub.Subscription, id peer.ID) {
	for {
		m, err := sub.Next(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			log.Println("Error receiving message:", err)
			continue
		}

		var e entry
		err = msgpack.Unmarshal(m.Data, &e)
		if err != nil {
			log.Println("Error unmarshalling message: ", err)
			continue
		}
		if e.Source != id {
			clipboard.Write(clipboard.FmtText, []byte(e.Value))
		}
	}
}
