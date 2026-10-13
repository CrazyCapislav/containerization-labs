# Релиз v2.8.0

Диапазон: v2.7.0 .. v2.8.0, коммитов: 18

## Требует внимания дежурного
- Миграция: индекс по created_at в orders — блокирует запись при прогоне. https://github.com/CrazyCapislav/containerization-labs/commit/7dffd7a
- Миграция: колонка idempotency_key в payments — меняет схему, нужна проверка отката. https://github.com/CrazyCapislav/containerization-labs/commit/804e298
- Оплата: таймаут банка 3→8 с, затем откат — ретраи ломались, следить за возвратами. https://github.com/CrazyCapislav/containerization-labs/commit/5f5978f
- Хотфикс: возврат средств при частичной отмене заказа — риск повторного списания. https://github.com/CrazyCapislav/containerization-labs/commit/db9d4d5
- Заказы: валидация адреса до создания — возможен рост отказов. https://github.com/CrazyCapislav/containerization-labs/commit/3dcfa84

## Что увидят клиенты и курьеры
Корзина собирается быстрее, лишний запрос к каталогу убран. Курьеров назначают с учётом загруженности. Повторный пуш при ретрае доставки больше не слать. Адрес проверяется до создания заказа.

## Внутренние изменения
Рефакторинг LRU-кэша; CI по тегу; реплики заказов 4→6; флаг new_checkout выключен; метрики-гистограммы; тесты назначения курьера; pgx 5.11.0; доки по лимитам API; логирование причины отклонения заказа.

## Миграции базы данных
1. Индекс по created_at в orders.
2. Колонка idempotency_key в payments.
