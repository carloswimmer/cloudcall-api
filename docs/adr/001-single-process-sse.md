# ADR 001 — Um único processo, hub SSE em memória, PostgreSQL como fonte da verdade

- Status: aceito
- Contexto: demonstração CloudCall Desk (API Go + cliente Angular)

## Contexto

O cliente precisa receber, em tempo real, mudanças de chamadas e de presença
(`GET /api/v1/events`, Server-Sent Events). A API é uma demonstração pedagógica:
um dono de chamadas (o usuário demo), uma organização e uma instância do servidor.

## Decisão

1. **Um único processo da API.** `cmd/api` sobe um `http.Server` e nada mais.
2. **Hub SSE em memória** (`internal/event.Hub`). Cada cliente conectado tem uma
   fila limitada (32 eventos). A publicação nunca bloqueia: se a fila de um
   cliente enche, só esse cliente é desconectado e, ao reconectar, recebe um
   snapshot novo.
3. **PostgreSQL é a fonte da verdade.** O hub guarda apenas conexões abertas e
   filas, nunca estado de negócio. Os eventos são publicados **depois** do
   `Commit`; o snapshot enviado ao conectar é lido do banco (chamadas ativas,
   usuários e tombstones recentes). Perder o hub, ou reiniciar o processo, não
   perde dados.
4. **Reinício = varredura.** Chamadas não terminais deixadas por um processo
   anterior não têm mais timers de simulação; `SweepInterrupted` as marca como
   `failed` com `simulation_interrupted` antes de o servidor escutar.
5. **Encerramento gracioso (10 s).** Em SIGINT/SIGTERM: `http.Server.Shutdown`
   (que fecha o hub assim que começa, para os streams SSE terminarem e não
   segurarem o desligamento) e depois `App.Shutdown` (hub, depois `db.Close()`).

## Consequências

- Simples de entender, testar e rodar (um binário + um PostgreSQL).
- Não há `WriteTimeout` global no servidor, pois mataria os streams SSE; cada
  escrita SSE define seu próprio prazo.
- A garantia de "uma chamada ativa por dono" e as transições concorrentes vêm do
  banco (índice único parcial, `SELECT … FOR UPDATE`, versão otimista), não do
  processo. Isso continua correto mesmo com mais de uma instância.

## Limites e alternativa para várias instâncias

Com **mais de uma instância**, o hub em memória deixa de bastar: um evento
publicado na instância A não chega aos clientes SSE conectados na instância B, e
a varredura de inicialização de uma instância falharia chamadas que outra ainda
está servindo. Para escalar horizontalmente seria necessário outro mecanismo de
distribuição de eventos (por exemplo `LISTEN/NOTIFY` do PostgreSQL ou um broker
como Redis/NATS) e um critério de dono para a varredura (lease ou heartbeat por
instância). Isso está fora do escopo desta demonstração.
