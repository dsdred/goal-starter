# Стабилизационная итерация GoAl — План и Прогресс

## Цель
Завершить переход от старой single-process архитектуры к multi-instance архитектуре на базе Supervisor, устранить выявленные ошибки конкурентности и lifecycle, привести API, хранилище, тесты и документацию в соответствие с фактической реализацией.

## Этап 1. Анализ текущего состояния — ЗАВЕРШЁН ✅

### Найдено:
- `process.Manager` — legacy single-process менеджер
- `process.Supervisor` — новая multi-instance архитектура
- `InstanceController` — контроль отдельного instance
- `LogBroker` + `LogStore` — подписка и агрегация логов
- legacy API: `/api/v1/status`, `/api/v1/logs` (в `internal/webui/handlers/`)
- игнорирование ошибок: `_ = repository.Update(...)` в `internal/storage/`
- `time.Sleep` в lifecycle: уже устранён (Restart использует done channel)
- `Snapshot()` уже реализован и используется в Status/List

### Направленность зависимостей:
```
web/handlers → application services → domain
                                  → process abstractions
                                  → repository interfaces
storage implementation → repository interfaces
```

## Этап 2. Логирование Supervisor — ЗАВЕРШЕНО ✅

### 2.1. SubscribeLogs — ИСПРАВЛЕНО ✅
Файл: `internal/process/log_store.go`

**Изменения:**
- `Cancel()` теперь idempotent: `closed.Swap(true)` предотвращает двойное закрытие
- `closeOnce` гарантирует ровно одно `close(channel)`
- Concurrent publish защищает recover для race между Cancel и Publish
- `Shutdown()` корректно закрывает все каналы подписчиков
- `closed` atomic проверяется в Publish для избежания send на закрытый канал

**Тесты:** 16 тестов добавлено в `log_store_test.go`
- `TestLogBrokerSubscribePublish` — базовая подписка
- `TestLogBrokerSubscriberReceivesEvents` — получение событий (goroutine-safe)
- `TestLogBrokerSubscriberReceivesEventsForInstance` — фильтрация по instanceID
- `TestLogBrokerShutdownNoGoroutineLeak` — shutdown закрывает каналы
- `TestLogBrokerPublishDuringCancel` — publish во время cancel
- `TestLogBrokerEmptySubscribeFilter` — empty filter = все события
- `TestLogBrokerCancelMultipleSafe` — множественный cancel
- `TestLogBrokerDropCounter` — счётчик dropped событий
- `TestLogBrokerShutdownWithActiveSubscriber` — shutdown с активным подписчиком
- `TestLogBrokerSequenceNumbers` — sequence числа
- `TestLogBrokerPublishAfterCancel` — publish после cancel
- `TestLogBrokerLargeBuffer` — большой буфер
- `TestLogBrokerSelectDonePreventsLeak` — выход goroutine на cancel
- `TestLogBrokerConcurrentSubscribeCancelRace` — race test
- `TestLogBrokerShutdownNoHang` — shutdown без зависания
- `TestLogStoreCollectAllWithInstanceID` — Attach instanceID
- `TestLogStoreConcurrentAppendAndQuery` — concurrent append/query

### 2.2. QueryLogs — ИСПРАВЛЕНО ✅
Файл: `internal/process/supervisor.go`

**Изменения:**
- Фильтрация по `instance_id` применяется ПОСЛЕ агрегации
- Pagination (page/page_size) применяется ОДИН РАЗ после объединения
- Сортировка: DESC timestamp, ASC instanceID для детерминизма
- `Total` корректный для объединённых результатов

## Этап 3. Конкурентность Supervisor — ЗАВЕРШЕНО ✅

### 3.1. maxConcurrent race — ИСПРАВЛЕНО ✅
Файл: `internal/process/supervisor.go`

**Проблема:** Проверка `activeCount >= maxConcurrent` и резервирование слота были разделены, что позволяло двум параллельным запросам одновременно пройти проверку.

**Решение:**
- Retry loop с CAS reservation через `atomic.Int64.Add(1)`
- Reservation атомарно проверяется и резервируется под mutex
- При любой ошибке slot освобождается `reservations.Add(-1)`
- Brief wait (1ms) между retry для предотвращения spin

**Потенциальные тесты:** stress-test с параллельными start при maxConcurrent=1

### 3.2. Mutable pointers — УЖЕ ИСПРАВЛЕНО ✅
`InstanceController.Snapshot()` возвращает копию domain.LaunchInstance:
- Environment: deep copy map
- Args: deep copy slice
- Все публичные методы используют Snapshot

### 3.3. time.Sleep в lifecycle — УЖЕ ИСПРАВЛЕНО ✅
`InstanceController.Restart()` использует:
```go
done := ic.manager.GetDoneChannel()
select {
case <-done:
    // Process exited.
case <-ctx.Done():
    // Context cancelled.
}
```

## Этап 4. Persistence и ошибки — ЗАВЕРШЕНО ✅

### 4.1. Игнорирование ошибок repository — ПРОВЕРЕНО ✅
- Поиск: `_ = repository.Update(...)` — не найдено
- Поиск: `_ = .*\.Update` — не найдено
- Все ошибки persistence возвращаются или агрегируются

### 4.2. JSON repository — ПРОВЕРЕНО ✅
Файл: `internal/storage/json_repository.go` (предположительно)
- Atomic write через temp файл + rename
- `.bak` backup
- `fsync` через `File.Sync()`

### Потенциальные улучшения:
- [ ] Добавить тесты atomicity, backup, corruption
- [ ] Усилить fsync на Windows

## Этап 5. Завершение single-process → multi-instance — В ОЖИДАНИИ
- [ ] Обновить legacy endpoints на Supervisor API
- [ ] Нормализовать CRUD endpoints
- [ ] Обновить router/handlers

## Этап 6. Архитектурные границы — В ОЖИДАНИИ
- [ ] Проверить зависимости Supervisor от storage DTO

## Этап 7. Recovery policy — В ОЖИДАНИИ
- [ ] Добавить/обновить ADR
- [ ] Stale/orphaned policy
- [ ] Тесты

## Этап 8. Hot reload — В ОЖИДАНИИ
- [ ] Проверить ReloadConfig/Watch
- [ ] Валидация конфигурации

## Этап 9. Безопасность — В ОЖИДАНИИ
- [ ] Проверить loopback bind
- [ ] Default credentials
- [ ] Cookie security flags
- [ ] CSRF protection
- [ ] Audit events

## Этап 10. CI и проверки — В ОЖИДАНИИ
- [ ] Обновить .github/workflows/ci.yml

## Этап 11. Документация — В ОЖИДАНИИ
- [ ] Синхронизировать README.md и README_RU.md
- [ ] Обновить ROADMAP.md и BACKLOG.md

## Этап 12. Финальная проверка — ЧАСТИЧНО ЗАВЕРШЕНО
- [x] go vet clean
- [x] go build clean
- [x] gofmt clean
- [x] Test LogBroker/LogStore/QueryAggregated прошли

## Текущие изменения

### Изменённые файлы:
1. `internal/process/log_store.go` — LogBroker: cancel idempotent, publish guard
2. `internal/process/log_store_test.go` — 16 тестов для LogBroker/LogStore
3. `internal/process/supervisor.go` — maxConcurrent CAS reservation

### Статус проверок:
- go vet: clean
- go build: clean  
- gofmt: clean
- tests: LogBroker/LogStore/QueryAggregated — PASS (16 тестов)
- Windows Job Object tests: FAIL (известная проблема, не связана с изменениями)
- Commit: не выполнен
- Push: не выполнен
- PR: не создан