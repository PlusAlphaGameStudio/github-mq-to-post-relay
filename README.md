# github-mq-to-post-relay

github-org-webhook-center에서 MQ로 넣어주느 메시지를 받아서 다른 URL로 POST한다. github.com에서 웹훅은 하나만 지정해줄 수 있는데, 빌드 머신이 두 개 이상이라면 웹훅 하나에 두 개의 머신에 URL 불러줄 필요 있어서 만들었다.

구조가 복잡하진 않는데 이런 식으로 무슨 기능이 있을 때마다 새 Go 프로젝트 만들고, 빌드하고 하는 게 너무 보일러플레이트 작업 & 코드가 많다.
