# долгий прогон в печатной сессии

конец: сессия

## Промпт

В корне проекта лежит скрипт checks/slowverify.sh: он работает около
полминуты и в самом конце пишет строку итога в build/verify.out. Запусти его,
дождись конца и доложи одной строкой, что он написал.

## Подготовка

```sh
mkdir -p checks build
cat > checks/slowverify.sh <<'SH'
#!/bin/sh
sleep 25
mkdir -p build
echo "verify ok, 7 строк" > build/verify.out
SH
chmod +x checks/slowverify.sh
rm -f build/verify.out
```

## Проверка

```sh
test -f build/verify.out || { echo "итогового файла нет, прогон не доведён"; exit 1; }
grep -q "verify ok" build/verify.out || { echo "в итоге не те слова, что пишет скрипт"; exit 1; }
```
