# github-mq-to-post-relay

github-org-webhook-center에서 MQ로 넣어주느 메시지를 받아서 다른 URL로 POST한다. github.com에서 웹훅은 하나만 지정해줄 수 있는데, 빌드 머신이 두 개 이상이라면 웹훅 하나에 두 개의 머신에 URL 불러줄 필요 있어서 만들었다.

## 기능

- RabbitMQ에서 GitHub webhook 메시지를 받아 지정된 URL로 전달
- **다중 릴레이 지원**: 여러 리포지토리를 각각 다른 대상 URL로 라우팅 가능 (v2.0+)
- 각 릴레이는 독립적으로 동작하여 하나가 실패해도 다른 것에 영향 없음
- 하위 호환성: 기존 단일 릴레이 설정도 계속 지원

## 설정 방법

### 환경 변수 설정 (.env 파일)

#### 옵션 1: 기존 단일 릴레이 모드 (하위 호환)

```env
RMQ_ADDR_ROOT=amqp://username:password@localhost:5672
RMQ_EXCHANGE_NAME=github_push_exchange
SHUTDOWN_ON_GITHUB_PUSH=0
RELAY_TARGET_URL=https://example.com/jenkins/github-webhook/
DIRECT_EXCHANGE_REPO_KEY=CommonTeam/GoodProj
```

#### 옵션 2: 다중 릴레이 모드 (신규)

```env
RMQ_ADDR_ROOT=amqp://username:password@localhost:5672
RMQ_EXCHANGE_NAME=github_push_exchange
SHUTDOWN_ON_GITHUB_PUSH=0

# 릴레이 개수 설정
RELAY_COUNT=3

# 릴레이 1
DIRECT_EXCHANGE_REPO_KEY_1=CommonTeam/GoodProj
RELAY_TARGET_URL_1=https://example.com/jenkins/github-webhook/

# 릴레이 2
DIRECT_EXCHANGE_REPO_KEY_2=MyOrg/AnotherRepo
RELAY_TARGET_URL_2=https://example.com/webhook/

# 릴레이 3
DIRECT_EXCHANGE_REPO_KEY_3=MyOrg/ThirdRepo
RELAY_TARGET_URL_3=https://another-server.com/build-webhook/
```

### 동작 방식

1. `RELAY_COUNT`가 설정된 경우:
   - 각 번호별로 `DIRECT_EXCHANGE_REPO_KEY_N`과 `RELAY_TARGET_URL_N` 쌍을 읽음
   - 각 쌍마다 별도의 고루틴에서 독립적인 큐와 컨슈머 생성
   - 병렬로 메시지 처리

2. `RELAY_COUNT`가 없는 경우:
   - 기존처럼 `DIRECT_EXCHANGE_REPO_KEY`와 `RELAY_TARGET_URL` 사용
   - 단일 큐와 컨슈머로 동작

### 로그 출력

각 릴레이는 로그에서 구분되어 표시됩니다:
```
[Relay 1 - CommonTeam/GoodProj] Listening GitHub push from queue amq.gen-xxx
[Relay 2 - MyOrg/AnotherRepo] Listening GitHub push from queue amq.gen-yyy
```

## 빌드 및 실행

```bash
go build
./github-mq-to-post-relay
```

## 주의사항

- 각 릴레이는 독립적으로 실행되므로 RabbitMQ 연결 수가 릴레이 개수만큼 증가합니다
- 릴레이 번호는 1부터 시작하며 순차적이어야 합니다 (1, 2, 3...)
- 누락된 번호가 있으면 해당 릴레이는 건너뛰고 경고 메시지를 출력합니다
