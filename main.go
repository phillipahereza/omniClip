package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/libp2p/go-libp2p"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"
	"github.com/urfave/cli/v2"
	"github.com/vmihailenco/msgpack/v5"
	"golang.design/x/clipboard"
	"golang.org/x/sync/singleflight"
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

// messageCache is a thread-safe cache for storing message hashes
type messageCache struct {
	sync.RWMutex
	cache map[string]struct{}
}

// config struct for omniclip settings
type config struct {
	TopicName   string
	ServicePort int
	StatusPort  int
	LogLevel    string
}

func hashContent(content string) string {
	hasher := xxhash.New()
	hasher.WriteString(content)
	return hex.EncodeToString(hasher.Sum(nil))
}

// cleaner periodically clears the message cache to prevent it from growing indefinitely.
func (mc *messageCache) cleaner(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			mc.clearCache()
		}
	}

}

func (mc *messageCache) addEntry(entry string) bool {
	key := hashContent(entry)
	mc.Lock()
	defer mc.Unlock()

	if _, ok := mc.cache[key]; ok {
		return false
	}
	mc.cache[key] = struct{}{}
	return true
}

func (mc *messageCache) contains(entry string) bool {
	key := hashContent(entry)
	mc.RLock()
	defer mc.RUnlock()

	_, ok := mc.cache[key]
	return ok
}

func (mc *messageCache) clearCache() {
	mc.Lock()
	defer mc.Unlock()
	mc.cache = make(map[string]struct{})
}

func newMessageCache(ctx context.Context) *messageCache {
	mc := &messageCache{cache: make(map[string]struct{})}
	go mc.cleaner(ctx)
	return mc
}

func main() {
	var loglevel string
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
					return getStatus(&config{StatusPort: defaultStatusPort})
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
					&cli.StringFlag{
						Name:        "loglevel",
						Aliases:     []string{"l"},
						Usage:       "Set the logging level (DEBUG, INFO, ERROR, WARN).",
						Destination: &loglevel,
						EnvVars:     []string{"OMNICLIP_LOGLEVEL"},
						Value:       "ERROR",
					},
				},
				Action: func(c *cli.Context) error {
					cfg := &config{
						TopicName:   c.String("topic"),
						ServicePort: c.Int("port"),
						StatusPort:  c.Int("status"),
						LogLevel:    c.String("loglevel"),
					}
					return startNode(c.Context, cfg)
				},
				Before: func(c *cli.Context) error {
					var handler slog.Handler
					switch loglevel {
					case "DEBUG":
						handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
					case "INFO":
						handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
					case "WARN":
						handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})
					case "ERROR":
						handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})
					default:
						handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})
					}
					slog.SetDefault(slog.New(handler))
					return nil
				},
			},
		},
		ErrWriter: io.Discard,
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal(err)
	}
}

// getStatus retrieves and prints the status of the omniclip node.
func getStatus(cfg *config) error {
	client := http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://0.0.0.0:%d/status", cfg.StatusPort))
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

func initServer(ctx context.Context, cfg *config, tp *pubsub.Topic, h host.Host) *http.Server {
	logger := slog.Default()
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
			logger.Error("Error encoding status response", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
	})

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.StatusPort),
		Handler:           mux,
		ReadHeaderTimeout: 1 * time.Second,
	}

	go func() {
		<-ctx.Done()
		slog.Info("Shutting down status server")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("Error shutting down status server", "error", err)
		}
	}()

	return server
}

func startNode(ctx context.Context, cfg *config) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	logger := slog.Default()

	// Create libp2p host
	h, err := libp2p.New(libp2p.ListenAddrStrings(fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", cfg.ServicePort)))
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

	topic, err := ps.Join(cfg.TopicName)
	if err != nil {
		return fmt.Errorf("error joining pubsub topic: %w", err)
	}

	subscription, err := topic.Subscribe()
	if err != nil {
		return fmt.Errorf("error subscribing to pubsub topic: %w", err)
	}

	// Start status server
	server := initServer(ctx, cfg, topic, h)
	errChan := make(chan error, 1)

	go func() {
		logger.Info("Starting status server", "port", cfg.StatusPort)
		errChan <- server.ListenAndServe()
	}()

	// Start peer discovery
	if err = setupDNSDiscovery(h); err != nil {
		return fmt.Errorf("error setting up DNS discovery: %w", err)
	}

	// Start Clipboard Watcher and Message Receiver
	cache := newMessageCache(ctx)
	sf := singleflight.Group{}
	go watchClipboard(ctx, topic, clipboard.Watch(ctx, clipboard.FmtText), h.ID(), cache, &sf)

	go receiveMessages(ctx, subscription, h.ID(), cache, &sf)

	// Handle graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-stop:
		logger.Info("Shutting down...")
		cancel()
		return nil

	case svErr := <-errChan:
		logger.Error("Server error", "error", svErr)
		cancel()
		return svErr
	}
}

// setupDNSDiscovery sets up mDNS discovery for the libp2p host.
func setupDNSDiscovery(h host.Host) error {
	s := mdns.NewMdnsService(h, serviceName, &discoverer{h: h})
	return s.Start()
}

// HandlePeerFound is called when a new peer is discovered via mDNS.
// It attempts to connect to the peer if it's not the host itself.
func (n *discoverer) HandlePeerFound(pi peer.AddrInfo) {
	logger := slog.Default()

	if pi.ID == n.h.ID() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	logger.Info("Discovered peer", "peerID", pi.ID.ShortString())

	err := n.h.Connect(ctx, pi)
	if err != nil {
		logger.Warn("error connecting to peer", "peerID", pi.ID.ShortString(), "error", err)
	}
}

// watchClipboard monitors the clipboard for changes and publishes new entries to the pubSub topic.
func watchClipboard(ctx context.Context, topic *pubsub.Topic, clipboardCh <-chan []byte, id peer.ID, mc *messageCache, sf *singleflight.Group) {
	logger := slog.Default()
	for msg := range clipboardCh {
		// Use singleflight to debounce clipboard updates
		_, err, _ := sf.Do("clipboardUpdate", func() (interface{}, error) {
			// check if the content has already been seen. If so, skip publishing
			logger.Info("Clipboard updated", "text", string(msg))
			e := &entry{Value: string(msg), Source: id}

			if !mc.addEntry(e.Value) {
				return nil, nil // Skip publishing if already in cache
			}

			data, err := msgpack.Marshal(e)
			if err != nil {
				return nil, fmt.Errorf("marshalling clipboard entry: %w", err)
			}

			// Publish the serialized entry to the pubSub topic
			if err := topic.Publish(ctx, data); err != nil {
				return nil, fmt.Errorf("publishing clipboard entry: %w", err)
			}
			return nil, nil
		})

		if err != nil {
			logger.Warn("Error in clipboard update:", "error", err)
		}

	}
}

// receiveMessages receives messages from the pubSub topic, unmarshals them,
// and updates the local clipboard if the message originated from a different peer.
func receiveMessages(ctx context.Context, sub *pubsub.Subscription, id peer.ID, mc *messageCache, sf *singleflight.Group) {
	logger := slog.Default()
	for {
		m, err := sub.Next(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			logger.Warn("Error receiving message", "error", err)
			continue
		}

		var e entry
		err = msgpack.Unmarshal(m.Data, &e)
		if err != nil {
			logger.Warn("Error unmarshalling message", "error", err)
			continue
		}

		if e.Source == id { // ignore messages that originated from the current node
			continue
		}

		if mc.contains(e.Value) { // ignore messages that have already been seen
			continue
		}

		_, err, _ = sf.Do("clipboardWrite", func() (interface{}, error) {
			if e.Source != id {
				clipboard.Write(clipboard.FmtText, []byte(e.Value))
			}
			return nil, nil
		})

		if err != nil {
			logger.Warn("Error writing to clipboard:", "error", err)
		}
	}
}
