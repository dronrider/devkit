# Откуда взяты образцы

Образцы собраны из публичной документации Jira Cloud platform REST API v3, а не
сняты с живого инстанса: в корпоративный трекер за ними не ходили, и ничего
своего (адресов, ключей проектов, имён, `customfield`) в них нет. Плейсхолдеры
`your-domain.atlassian.net`, ключ `ED-1` и «Mia Krystof» это те же плейсхолдеры,
что стоят в примерах Atlassian. Отсюда и ограничение: формат живьём не проверен,
первое расхождение с настоящим инстансом покажется на нём, а не здесь.

| Файл | Страница доки | Что взято |
|---|---|---|
| `issue.json` | [Issues: get issue](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issues/#api-rest-api-3-issue-issueidorkey-get) | каркас ответа и `fields.timetracking` |
| `issue.json` | [Issues: bulk fetch](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issues/#api-rest-api-3-issue-bulkfetch-post) | `fields.summary` |
| `issue.json` | [Issue links: get issue link](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-links/#api-rest-api-3-issuelink-linkid-get) | `fields.status` и `fields.issuetype` |
| `transitions.json` | [Issues: get transitions](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issues/#api-rest-api-3-issue-issueidorkey-transitions-get) | ответ целиком, без секции `fields` перехода |
| `error-required-field.json` | [Issues: create issue, ответ 400](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issues/#api-rest-api-3-issue-post) | `errorMessages` |
| `error-fields.json` | [Issues: bulk create, ответ 400](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issues/#api-rest-api-3-issue-bulk-post) | тексты `errors` |
| `worklog-created.json` | [Issue worklogs](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-worklogs/#api-rest-api-3-issue-issueidorkey-worklog-post) | объект `Worklog` |
| `comment-created.json` | [Issue comments: add comment](https://developer.atlassian.com/cloud/jira/platform/rest/v3/api-group-issue-comments/#api-rest-api-3-issue-issueidorkey-comment-post) | ответ 201 |

Два образца собраны, а не скопированы одним куском, и это стоит держать в уме.
В примере «get issue» нет ни `summary`, ни `status`, ни `issuetype`, поэтому
`issue.json` склеен из трёх примеров того же справочника, поля взяты дословно.
Пример 400 с непустым `errors` дока показывает только у bulk create, где он
завёрнут в `elementErrors`; в `error-fields.json` те же тексты уложены в схему
`ErrorCollection`, которой отвечают операции с одним тикетом.
