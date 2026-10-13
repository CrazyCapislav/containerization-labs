# Мост между stdio-сервером filesystem и HTTP, который понимает n8n.
#
# Узел MCP Client Tool в n8n 2.42.7 умеет только httpStreamable и sse,
# а официальный сервер filesystem разговаривает через stdio. supergateway
# запускает его дочерним процессом и переводит протокол на HTTP.
#
# Пакеты ставятся на этапе сборки, а не через npx при каждом старте:
# иначе контейнер тратит около минуты на загрузку при каждом перезапуске.
FROM node:22-alpine

RUN npm install -g \
      supergateway@latest \
      @modelcontextprotocol/server-filesystem@latest \
 && npm cache clean --force

# Каталог, за пределы которого сервер не выпустит: он сам проверяет,
# что путь лежит внутри разрешённого, и это единственная его защита.
ENV MCP_ROOT=/workspace
EXPOSE 8001

CMD ["sh", "-c", "supergateway \
  --stdio 'mcp-server-filesystem /workspace' \
  --outputTransport streamableHttp \
  --streamableHttpPath /mcp \
  --port 8001 \
  --host 0.0.0.0"]
