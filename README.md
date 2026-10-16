# Лабораторные: контейнеризация и оркестрация

Работы по курсу [containerization-and-orchestration](https://github.com/KeladKaal/containerization-and-orchestration).

| Лаба | Тема | Статус |
|---|---|---|
| [lab1-docker](lab1-docker/) | Свой Docker: namespaces, cgroups, права | готово |
| [lab2-observability](lab2-observability/) | Мониторинг: метрики, логи, трейсы, алерты | готово |
| [lab3-platform](lab3-platform/) | Платформа: политики, Helm, оператор БД, отказ control plane | готово |
| [lab4-scheduling](lab4-scheduling/) | Планирование и ресурсы: разнесение, QoS, вытеснение, выселение, SLA | готово |
| [lab8-morning-digest](lab8-morning-digest/) | AI-инфраструктура: n8n, MCP-серверы, агент и сводка по релизу | готово |

Отдельно `monitoring/` — локальный стек Prometheus + Grafana на compose, им снимались метрики контейнера в первой лабе.

Среда: Ubuntu 24.04 в Hyper-V, cgroup v2, Go 1.22, Docker 29.1.
