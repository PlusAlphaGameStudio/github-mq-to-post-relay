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
	"strings"
	"time"
)

var shutdownCh chan string

// github-org-webhook-center에서 MQ로 넣어주느 메시지를 받아서 다른 URL로 POST한다.
// github.com에서 웹훅은 하나만 지정해줄 수 있는데, 빌드 머신이 두 개 이상이라면 웹훅 하나에 두 개의 머신에 URL 불러줄 필요 있어서 만들었다.

func main() {
	log.Println("github-mq-to-post-relay started")

	goDotErr := godotenv.Load()
	if goDotErr != nil {
		log.Println("Error loading .env file")
	}

	shutdownCh = make(chan string)

	for {
		err := listenForGitHubPush()
		if err != nil {
			const retryInterval = 60
			log.Printf("Error '%v' returned from listenForGitHubPush(). (Check github-org-webhook-center running!) Retry in %v seconds...", err, retryInterval)
			<-time.After(retryInterval * time.Second)
		}
	}
}

func listenForGitHubPush() error {
	// ADDR_'ROOT': 특정 virtual host 속한 것이 아니라 공용
	conn, err := amqp.Dial(os.Getenv("RMQ_ADDR_ROOT"))
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
		os.Getenv("DIRECT_EXCHANGE_REPO_KEY"),
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

	log.Printf("Listening GitHub push from %v\n", q.Name)

loop:
	for {
		select {
		case d := <-deliveries:
			if os.Getenv("SHUTDOWN_ON_GITHUB_PUSH") == "1" {
				shutdownCh <- "push from github"
			} else {
				log.Printf("Push from GitHub detected, but SHUTDOWN_ON_GITHUB_PUSH is not enabled. Ignored.")
			}

			postToUrl(d.Body)
		case <-shutdownCh:
			break loop
		case onCloseValue := <-onClose:
			// RMQ 접속 끊겼을 때
			return onCloseValue
		}
	}

	return nil
}

func postToUrl(jsonPayload []byte) {

	// 1. 폼 필드 정의
	form := url.Values{}
	form.Set("payload", string(jsonPayload))

	encoded := form.Encode()

	log.Println("====Payload Begin====")
	log.Println(string(encoded))
	log.Println("====Payload End====")

	// 2. Create request with context (here we give it a 10 s timeout)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, os.Getenv("RELAY_TARGET_URL"), io.NopCloser(strings.NewReader(encoded)))
	if err != nil {
		log.Println(fmt.Errorf("build request: %w", err))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Content-Length", fmt.Sprint(len(encoded))) // 선택(대부분 생략 가능)

	req.Header.Set("X-GitHub-Event", "push") // Jenkins에서 확인하는 꼭 필요한 헤더. 하드코딩!


	// 3. Send the request
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println(fmt.Errorf("do request: %w", err))
		return
	}

	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Printf(err.Error())
		}
	}(resp.Body)

	// 4. Quick status-code check
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Println(fmt.Errorf("received non-2xx status: %s", resp.Status))
		return
	}

	// 5. Read and print body (discard or parse as needed)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println(fmt.Errorf("read body: %w", err))
		return
	}

	fmt.Printf("Server replied (%s):\n%s\n", resp.Status, body)
}