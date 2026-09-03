# Команды тредов ревью

Файл читается только когда собеседник человек и канал это тред трекера:
GitLab, GitHub или Gitea. Разговор с агентом идёт другим каналом. Пауза
между публикациями (десятки секунд) держится циклом со sleep, не пачкой
POST за секунду подряд.

Пачку замечаний чужого ревью носит в MR `taskctl review publish` по файлу
`docs/tasks/<ID>.review.md`: те же запросы, но в формах, которые отдают JSON,
и id треда из ответа команда вписывает в файл. Руками эти команды нужны
там, где ответ в тред и резолв, публикацией занимается утилита.

Плейсхолдеры в угловых скобках. Токен только именем переменной окружения,
значение берётся из secretctl devkit, в команду оно не подставляется. Тело
замечания в формате conventional comments: `issue: суть проблемы` или
`suggestion (non-blocking): предложение`.

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
Токен ставит `gh auth login`. У сырого curl заголовок `Authorization: Bearer $GITHUB_TOKEN` и `Accept: application/vnd.github+json`. Резолв треда живёт только в GraphQL, в REST такого метода нет вовсе.

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
Заголовок `Authorization: token $GITEA_TOKEN`, слово token обязательно, Bearer тут для другого вида токенов. `<index>` номер PR внутри репозитория.

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

## Чего нет
- GitLab: список тредов не фильтруется резолв-статусом на сервере, фильтр только на клиенте через jq, как в команде выше.
- GitHub: резолва треда нет в REST API вовсе, только GraphQL мутация resolveReviewThread. Апрув в REST это тот же POST .../reviews с event=APPROVE, gh pr review --approve делает ровно это.
- Gitea: резолв и ответ в тред это недавнее добавление API, до него методов не было вовсе. На старой инсталляции эндпойнтов /resolve и /replies может не быть, версию сервера проверить заранее через `GET /api/v1/version`.
