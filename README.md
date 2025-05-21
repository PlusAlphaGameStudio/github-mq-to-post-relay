# github-mq-to-post-relay

github-org-webhook-center에서 MQ로 넣어주느 메시지를 받아서 다른 URL로 POST한다. github.com에서 웹훅은 하나만 지정해줄 수 있는데, 빌드 머신이 두 개 이상이라면 웹훅 하나에 두 개의 머신에 URL 불러줄 필요 있어서 만들었다.
