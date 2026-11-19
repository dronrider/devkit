# Команды тредов ревью

Открой раздел нужного трекера для публикации, ответа или резолва.
Порядок пакетной публикации и паузы задаёт [foreign.md](foreign.md).
Для ручной публикации действуют разрешения проекта. Если публикует
владелец, передай ему текст замечаний.

Перед публикацией проверь открытые треды и метки по [SKILL.md](SKILL.md).
Плейсхолдеры обозначены угловыми скобками. Токены бери через `secretctl`
и передавай именами переменных окружения.

## GitLab
Заголовок `PRIVATE-TOKEN: $GITLAB_TOKEN`. `<id>` id проекта или namespace/path, `<iid>` номер MR. Три sha берёт поле `diff_refs` из `GET .../merge_requests/<iid>`.

Список открытых тредов:
```bash
curl -sS --header "PRIVATE-TOKEN: $GITLAB_TOKEN" \
  "https://<host>/api/v4/projects/<id>/merge_requests/<iid>/discussions" \
  | jq '.[] | select(.notes[0].resolvable and (.notes[0].resolved|not))
        | {id, body: .notes[0].body}'
```
Новый тред на строку диффа:
```bash
curl -sS --request POST --header "PRIVATE-TOKEN: $GITLAB_TOKEN" \
  "https://<host>/api/v4/projects/<id>/merge_requests/<iid>/discussions" \
  --data-urlencode "body=<текст>" \
  --data-urlencode "position[position_type]=text" \
  --data-urlencode "position[base_sha]=<base_sha>" \
  --data-urlencode "position[start_sha]=<start_sha>" \
  --data-urlencode "position[head_sha]=<head_sha>" \
  --data-urlencode "position[new_path]=<путь>" \
  --data-urlencode "position[new_line]=<номер_строки>"
```
Новый тред без строки, итог ревью:
```bash
curl -sS --request POST --header "PRIVATE-TOKEN: $GITLAB_TOKEN" \
  "https://<host>/api/v4/projects/<id>/merge_requests/<iid>/discussions" \
  --data-urlencode "body=<текст>"
```
Ответ в тред:
```bash
curl -sS --request POST --header "PRIVATE-TOKEN: $GITLAB_TOKEN" \
  "https://<host>/api/v4/projects/<id>/merge_requests/<iid>/discussions/<discussion_id>/notes" \
  --data-urlencode "body=<текст>"
```
Резолв треда:
```bash
curl -sS --request PUT --header "PRIVATE-TOKEN: $GITLAB_TOKEN" \
  "https://<host>/api/v4/projects/<id>/merge_requests/<iid>/discussions/<discussion_id>" \
  --data-urlencode "resolved=true"
```
Апрув MR:
```bash
curl -sS --request POST --header "PRIVATE-TOKEN: $GITLAB_TOKEN" \
  "https://<host>/api/v4/projects/<id>/merge_requests/<iid>/approve"
```

## GitHub
Используй `gh auth login`. Резолв и `isResolved` доступны через GraphQL.

Список открытых тредов, только GraphQL даёт isResolved:
```bash
gh api graphql -F owner=<owner> -F repo=<repo> -F pr=<номер> -f query='
  query($owner:String!,$repo:String!,$pr:Int!){
    repository(owner:$owner,name:$repo){
      pullRequest(number:$pr){
        reviewThreads(first:100){nodes{id isResolved path
          comments(first:1){nodes{body}}}}}}}'
```
Новый тред на строку диффа:
```bash
gh api repos/<owner>/<repo>/pulls/<номер>/comments \
  -f body="<текст>" -f commit_id="<sha>" -f path="<путь>" \
  -F line=<номер_строки> -f side=RIGHT
```
Новый тред без строки, итог ревью:
```bash
gh pr review <номер> --repo <owner>/<repo> --comment --body "<текст>"
```
Ответ в тред, `<comment_id>` это id конкретного комментария, не id треда:
```bash
gh api repos/<owner>/<repo>/pulls/<номер>/comments/<comment_id>/replies \
  -f body="<текст>"
```
Резолв треда, `<thread_id>` вида PRRT_xxx из списка выше:
```bash
gh api graphql -F threadId="<thread_id>" -f query='
  mutation($threadId:ID!){
    resolveReviewThread(input:{threadId:$threadId}){thread{isResolved}}}'
```
Апрув MR:
```bash
gh pr review <номер> --repo <owner>/<repo> --approve --body "<текст>"
```

## Gitea
Заголовок `Authorization: token $GITEA_TOKEN`. `<index>` номер PR. Для старой
инсталляции проверь поддержку `/resolve` и `/replies` через `GET /api/v1/version`.

Список тредов, резолв-статус лежит в comments каждого review, поле resolver:
```bash
curl -sS --header "Authorization: token $GITEA_TOKEN" \
  "https://<host>/api/v1/repos/<owner>/<repo>/pulls/<index>/reviews" \
  | jq '.[] | {id, body, state}'
```
Новый тред на строку диффа, привязка через new_position или old_position, поля side тут нет:
```bash
curl -sS --request POST --header "Authorization: token $GITEA_TOKEN" \
  --header "Content-Type: application/json" \
  "https://<host>/api/v1/repos/<owner>/<repo>/pulls/<index>/reviews" \
  --data '{"commit_id":"<sha>","event":"COMMENT",
    "comments":[{"path":"<путь>","new_position":<номер_строки>,"body":"<текст>"}]}'
```
Новый тред без строки, итог ревью:
```bash
curl -sS --request POST --header "Authorization: token $GITEA_TOKEN" \
  --header "Content-Type: application/json" \
  "https://<host>/api/v1/repos/<owner>/<repo>/pulls/<index>/reviews" \
  --data '{"event":"COMMENT","body":"<текст>"}'
```
Ответ в тред, `<comment_id>` id комментария review:
```bash
curl -sS --request POST --header "Authorization: token $GITEA_TOKEN" \
  --header "Content-Type: application/json" \
  "https://<host>/api/v1/repos/<owner>/<repo>/pulls/<index>/comments/<comment_id>/replies" \
  --data '{"body":"<текст>"}'
```
Резолв треда:
```bash
curl -sS --request POST --header "Authorization: token $GITEA_TOKEN" \
  "https://<host>/api/v1/repos/<owner>/<repo>/pulls/comments/<comment_id>/resolve"
```
Апрув MR:
```bash
curl -sS --request POST --header "Authorization: token $GITEA_TOKEN" \
  --header "Content-Type: application/json" \
  "https://<host>/api/v1/repos/<owner>/<repo>/pulls/<index>/reviews" \
  --data '{"event":"APPROVED"}'
```
