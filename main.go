package main

import (
	"context"
	"fmt"
	"github.com/joho/godotenv"
	amqp "github.com/rabbitmq/amqp091-go"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var shutdownCh chan string

// RelayConfig represents a single relay configuration pair
type RelayConfig struct {
	RepoKey   string // DIRECT_EXCHANGE_REPO_KEY - RabbitMQ routing key
	TargetURL string // RELAY_TARGET_URL - destination URL for webhook
	Index     int    // Configuration index for logging
}

// github-org-webhook-center에서 MQ로 넣어주느 메시지를 받아서 다른 URL로 POST한다.
// github.com에서 웹훅은 하나만 지정해줄 수 있는데, 빌드 머신이 두 개 이상이라면 웹훅 하나에 두 개의 머신에 URL 불러줄 필요 있어서 만들었다.

// loadRelayConfigs loads relay configurations from environment variables
// Supports both multi-relay (with RELAY_COUNT) and legacy single relay format
func loadRelayConfigs() []RelayConfig {
	var configs []RelayConfig

	// Check for multi-relay configuration
	relayCountStr := os.Getenv("RELAY_COUNT")
	if relayCountStr != "" {
		relayCount, err := strconv.Atoi(relayCountStr)
		if err != nil {
			log.Printf("Invalid RELAY_COUNT value: %s. Using legacy configuration.\n", relayCountStr)
			return loadLegacyConfig()
		}

		log.Printf("Loading %d relay configurations...\n", relayCount)
		for i := 1; i <= relayCount; i++ {
			repoKey := os.Getenv(fmt.Sprintf("DIRECT_EXCHANGE_REPO_KEY_%d", i))
			targetURL := os.Getenv(fmt.Sprintf("RELAY_TARGET_URL_%d", i))

			if repoKey == "" || targetURL == "" {
				log.Printf("Warning: Missing configuration for relay %d (repo_key=%s, target_url=%s). Skipping.\n",
					i, repoKey, targetURL)
				continue
			}

			config := RelayConfig{
				RepoKey:   repoKey,
				TargetURL: targetURL,
				Index:     i,
			}
			configs = append(configs, config)
			log.Printf("Relay %d configured: repo=%s, target=%s\n", i, repoKey, targetURL)
		}

		if len(configs) == 0 {
			log.Println("No valid relay configurations found. Falling back to legacy configuration.")
			return loadLegacyConfig()
		}
	} else {
		// Use legacy single relay configuration
		return loadLegacyConfig()
	}

	return configs
}

// loadLegacyConfig loads the legacy single relay configuration
func loadLegacyConfig() []RelayConfig {
	repoKey := os.Getenv("DIRECT_EXCHANGE_REPO_KEY")
	targetURL := os.Getenv("RELAY_TARGET_URL")

	if repoKey == "" || targetURL == "" {
		log.Fatal("No relay configuration found. Please set either RELAY_COUNT with numbered configurations or legacy DIRECT_EXCHANGE_REPO_KEY and RELAY_TARGET_URL")
	}

	log.Println("Using legacy single relay configuration")
	return []RelayConfig{{
		RepoKey:   repoKey,
		TargetURL: targetURL,
		Index:     0,
	}}
}

func main() {
	log.Println("github-mq-to-post-relay started")

	goDotErr := godotenv.Load()
	if goDotErr != nil {
		log.Println("Error loading .env file")
	}

	shutdownCh = make(chan string)

	// Load relay configurations
	configs := loadRelayConfigs()
	log.Printf("Loaded %d relay configuration(s)\n", len(configs))

	// Use WaitGroup to manage goroutines
	var wg sync.WaitGroup

	// Start a goroutine for each relay configuration
	for _, config := range configs {
		wg.Add(1)
		go func(cfg RelayConfig) {
			defer wg.Done()

			logPrefix := fmt.Sprintf("[Relay %d - %s]", cfg.Index, cfg.RepoKey)

			for {
				log.Printf("%s Starting listener...\n", logPrefix)
				err := listenForGitHubPush(cfg)
				if err != nil {
					const retryInterval = 60
					log.Printf("%s Error '%v' returned from listenForGitHubPush(). (Check github-org-webhook-center running!) Retry in %v seconds...",
						logPrefix, err, retryInterval)
					<-time.After(retryInterval * time.Second)
				}
			}
		}(config)
	}

	// Wait for all goroutines to complete (they won't in normal operation)
	wg.Wait()
}

func listenForGitHubPush(config RelayConfig) error {
	// ADDR_'ROOT': 특정 virtual host 속한 것이 아니라 공용
	amqpConfig := amqp.Config{Properties: amqp.NewConnectionProperties()}
	amqpConfig.Properties.SetClientConnectionName(fmt.Sprintf("github-mq-to-post-relay:%s", config.RepoKey))
	conn, err := amqp.DialConfig(os.Getenv("RMQ_ADDR_ROOT"), amqpConfig)
	if err != nil {
		return err
	}
	defer func(conn *amqp.Connection) {
		err := conn.Close()
		if err != nil {
			log.Printf("closing connection failed: %v\n", err)
		}
	}(conn)

	onClose := conn.NotifyClose(make(chan *amqp.Error))

	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	defer func(ch *amqp.Channel) {
		err := ch.Close()
		if err != nil {
			log.Printf("closing channel failed: %v\n", err)
		}
	}(ch)

	err = ch.Confirm(false)
	if err != nil {
		return err
	}

	queueName := ""

	q, err := ch.QueueDeclare(
		queueName,
		false,
		true,
		true,
		false,
		nil)
	if err != nil {
		return err
	}

	err = ch.QueueBind(
		q.Name,
		config.RepoKey,
		os.Getenv("RMQ_EXCHANGE_NAME"),
		false,
		nil,
	)
	if err != nil {
		return err
	}

	deliveries, err := ch.Consume(
		q.Name,
		"",
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return err
	}

	log.Printf("[Relay %d - %s] Listening GitHub push from queue %v\n", config.Index, config.RepoKey, q.Name)

loop:
	for {
		select {
		case d := <-deliveries:
			if os.Getenv("SHUTDOWN_ON_GITHUB_PUSH") == "1" {
				shutdownCh <- "push from github"
			} else {
				log.Printf("[Relay %d - %s] Push from GitHub detected, but SHUTDOWN_ON_GITHUB_PUSH is not enabled. Ignored.", config.Index, config.RepoKey)
			}

			postToUrl(d.Body, config.TargetURL, config.Index, config.RepoKey)
		case <-shutdownCh:
			break loop
		case onCloseValue := <-onClose:
			// RMQ 접속 끊겼을 때
			return onCloseValue
		}
	}

	return nil
}

func postToUrl(jsonPayload []byte, targetURL string, relayIndex int, repoKey string) {
	logPrefix := fmt.Sprintf("[Relay %d - %s]", relayIndex, repoKey)

	// 1. 폼 필드 정의
	form := url.Values{}
	form.Set("payload", string(jsonPayload))

	encoded := form.Encode()

	log.Printf("%s ====Payload Begin====", logPrefix)
	log.Println(string(encoded))
	log.Printf("%s ====Payload End====", logPrefix)

	// 2. Create request with context (here we give it a 10 s timeout)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, io.NopCloser(strings.NewReader(encoded)))
	if err != nil {
		log.Printf("%s %v", logPrefix, fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Content-Length", fmt.Sprint(len(encoded))) // 선택(대부분 생략 가능)

	req.Header.Set("X-GitHub-Event", "push") // Jenkins에서 확인하는 꼭 필요한 헤더. 하드코딩!


	// 3. Send the request
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("%s %v", logPrefix, fmt.Errorf("do request: %w", err))
		return
	}

	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Printf("%s %v", logPrefix, err)
		}
	}(resp.Body)

	// 4. Quick status-code check
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("%s %v", logPrefix, fmt.Errorf("received non-2xx status: %s", resp.Status))
		return
	}

	// 5. Read and print body (discard or parse as needed)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("%s %v", logPrefix, fmt.Errorf("read body: %w", err))
		return
	}

	log.Printf("%s Server replied (%s):\n%s\n", logPrefix, resp.Status, body)
}