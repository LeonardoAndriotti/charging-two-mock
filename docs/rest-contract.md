# Mocks de gateways ElecPay em Go

Três processos HTTP independentes, Go 1.22+, biblioteca padrão, sem banco ou credenciais reais. Contrato **ElecPay experimental v1**, não compatível com APIs Asaas, VoltBras, Monta ou OCPI. Consulte o estudo no repositório elecpay-docs.

## Executar

Referência original dos perfis (neste repositório use `make run`):

```sh
make wallet          # 127.0.0.1:9081
make charging-ocpi   # 127.0.0.1:9082, operador fictício A inspirado em OCPI
make charging-rest   # 127.0.0.1:9083, operador fictício B com polling
```

`MOCK_ADDR` altera o endereço de cada processo. Estado isolado em memória; reiniciar limpa tudo. Bind padrão em loopback. O token fixo de teste é `local-test-token`. Não publicar estes servidores em produção. Rotas `/_mock` são controles do teste e não devem ser chamadas pelos adaptadores do backend.

```sh
make check # testes com detector de race, build, vet e formatação
```

## Contrato HTTP

`GET /healthz` é público. Demais chamadas exigem `Authorization: Bearer local-test-token`. Todo POST exige JSON e `Idempotency-Key`. A mesma chave e os mesmos bytes de corpo/rota retornam a resposta original. Reutilizar chave com corpo ou rota diferente retorna 409. Chaves são globais por processo, sobrevivem apenas até reiniciar e respostas de erro não são armazenadas. Não existe expiração ou limpeza de chaves.

Valores financeiros positivos são inteiros em centavos BRL; limite por crédito/reserva de 100.000.000 centavos. Captura deve ser positiva e menor ou igual à reserva. Medição é acumulada, não decresce e limita a sessão a 1.000.000 Wh. O custo final arredonda para cima ao centavo: `(Wh * tarifa_centavos_kWh + 999) / 1000`.

| Serviço | Método e rota | Corpo / resultado |
|---|---|---|
| Todos | GET `/v1/capabilities` | Perfil e capacidades simuladas |
| Carteira | POST `/v1/wallets` | `{}` → carteira zerada, 201 |
| Carteira | GET `/v1/wallets/{id}` | Saldo disponível e reservado |
| Carteira | POST `/v1/topups` | `{"wallet_id":"wal_...","amount_cents":10000}` → operação PENDING, 201 |
| Teste financeiro | POST `/_mock/operations/{id}/confirm` | `{}` → confirma crédito |
| Carteira | GET `/v1/operations/{id}` | Estado da operação |
| Carteira | POST `/v1/holds` | `{"wallet_id":"wal_...","amount_cents":6000}` → HELD, 201 |
| Carteira | POST `/v1/holds/{id}/capture` | `{"amount_cents":2400}` → CAPTURED, libera sobra |
| Carteira | POST `/v1/holds/{id}/release` | `{}` → RELEASED |
| Carteira | POST `/v1/operations/{id}/refund` | `{}` → REFUNDED integral, uma vez |
| Recargas | GET `/v1/connectors` | `connector-1`, disponibilidade e tarifa |
| Recargas | POST `/v1/sessions` | `{"connector_id":"connector-1"}` → PENDING, 202 |
| Recargas | GET `/v1/sessions/{id}` | Estado e energia acumulada |
| Teste recarga | POST `/_mock/sessions/{id}/activate` | `{}` → ACTIVE |
| Teste recarga | POST `/_mock/sessions/{id}/meter` | `{"energy_wh":12000}` |
| Recargas | POST `/v1/sessions/{id}/stop` | `{}` → STOP_REQUESTED |
| Teste recarga | POST `/_mock/sessions/{id}/complete` | `{}` → COMPLETED e cálculo final |
| Operador A | GET `/v1/sessions/{id}/cdr` | Documento final simplificado |
| Operador B | GET `/v1/sessions/{id}/receipt` | Recibo final simplificado |
| Carteira e A | GET `/v1/events` | Array de eventos `{id,type,data}` |

Documentos finais retornam 404 antes de COMPLETED. Um único conector existe por operador; aceita apenas uma sessão não encerrada. O perfil B retorna 404 para `/v1/events`. A carteira e os dois operadores não compartilham dados.

Erros JSON: `{"error":"codigo"}`. Status: 400 para entrada inválida/chave ausente, 401 para token, 404 para recurso ausente, 405 para método inválido, 409 para conflito/estado/saldo, 422 para rejeição simulada, 503 para indisponibilidade. POSTs de transição respondem 200, criação 201 e início de sessão 202. Campos JSON desconhecidos e múltiplos objetos são rejeitados; envie apenas os campos documentados para cada operação. O decoder compartilhado reconhece os quatro campos de entrada da tabela, mas não exige exclusividade por rota.

## Exemplo reproduzível

```sh
curl -sS http://127.0.0.1:9081/v1/wallets \
  -H 'Authorization: Bearer local-test-token' \
  -H 'Idempotency-Key: wallet-1' -H 'Content-Type: application/json' -d '{}'

# Copie o id retornado para wallet_id.
curl -sS http://127.0.0.1:9081/v1/topups \
  -H 'Authorization: Bearer local-test-token' \
  -H 'Idempotency-Key: topup-1' -H 'Content-Type: application/json' \
  -d '{"wallet_id":"wal_000001","amount_cents":10000}'

curl -sS http://127.0.0.1:9082/v1/sessions \
  -H 'Authorization: Bearer local-test-token' \
  -H 'Idempotency-Key: start-1' -H 'Content-Type: application/json' \
  -d '{"connector_id":"connector-1"}'
```

Use os IDs retornados nas rotas de controle da tabela para avançar o fluxo. No operador B o mesmo início usa porta 9083. Com 12.000 Wh, A fecha em 2.400 centavos e B em 3.000. Antes de iniciar a recarga, o backend deve reservar saldo; após fechar, deve capturar o valor final. Os mocks não executam essa orquestração automaticamente.

## Falhas determinísticas

Adicione `X-Mock-Scenario` à chamada:

- `reject`: 422 sem alterar estado.
- `unavailable`: 503 sem alterar estado.
- `timeout`: espera três segundos antes de executar; se o cliente cancelar antes, não efetiva.
- `timeout-after-commit`: efetiva e armazena idempotência, mas espera três segundos para responder. Use timeout menor no cliente, consulte o recurso e repita sem cenário com a mesma chave para recuperar a resposta.

Nenhum cenário inclui aleatoriedade. Timeout após efetivação funciona para POST bem-sucedido; não significa cancelamento do comando remoto. Não há falha de hardware, cartão real, Pix real, OAuth, entrega de webhook ou homologação OCPI. A lista de eventos não tem paginação; use em testes pequenos e reinicie processos entre cenários independentes.
