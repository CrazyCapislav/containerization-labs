// Нагрузка на критический путь магазина: оформление заказа api -> postgres.
//
// Адрес берём через IP контейнера ноды: NodePort наружу виртуалки не
// опубликован, а docker-сеть 172.18.0.0/16 с хоста маршрутизируется.
//   BASE_URL  куда бить (по умолчанию нода server-0)
//   RPS       запросов в секунду
//   DURATION  сколько держать нагрузку
import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.BASE_URL || 'http://localhost:30006';

export const options = {
  scenarios: {
    orders: {
      // Открытая модель: k6 держит заданную частоту запросов независимо от
      // того, насколько медленно отвечает сервис. Закрытая модель (фиксированное
      // число VU) при замедлении сама снижает нагрузку и прячет деградацию —
      // ровно то, что нам нужно увидеть под перегрузкой.
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.RPS || 20),
      timeUnit: '1s',
      duration: __ENV.DURATION || '3m',
      preAllocatedVUs: 20,
      maxVUs: 200,
    },
  },
  // Это и есть формулировка SLA в машиночитаемом виде: k6 сам пометит прогон
  // провалившимся, если пороги не выдержаны.
  thresholds: {
    'http_req_duration{path:order}': ['p(95)<300'],
    'http_req_failed{path:order}': ['rate==0'],
  },
};

export default function () {
  const payload = JSON.stringify({ item: `товар-${__VU}-${__ITER}`, qty: 1 + (__ITER % 3) });

  const res = http.post(`${BASE}/order`, payload, {
    headers: { 'Content-Type': 'application/json' },
    tags: { path: 'order' },
  });

  check(res, { 'заказ создан (201)': (r) => r.status === 201 });

  // Чтение раз в десять итераций: путь заказа включает и просмотр списка,
  // но основная нагрузка должна идти на запись.
  if (__ITER % 10 === 0) {
    http.get(`${BASE}/orders`, { tags: { path: 'orders' } });
  }
}
