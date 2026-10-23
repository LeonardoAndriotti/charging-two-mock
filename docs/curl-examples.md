# curls para Postman — charging-two-mock

No Postman, selecione **Import → Raw text**, cole **um curl por vez** e importe. Cada bloco é uma requisição independente; não precisa de terminal, `jq` ou atribuições shell.

Você também pode importar [a collection pronta](../postman/collection.json), que contém todas as chamadas abaixo e captura automaticamente os IDs das respostas bem-sucedidas. Os curls importados individualmente exigem copiar os IDs manualmente.

Todos os endpoints, inclusive `/_mock`, estão disponíveis sem autenticação.

## Variáveis

Crie as variáveis na collection ou no ambiente selecionado:

| Variável | Valor inicial |
|---|---|
| `base_url` | `https://charging-two-mock.vercel.app` |
| `run_id` | `manual-001` |
| `session_id` | `` |
| `recovered_id` | `` |

Os curls abaixo já apontam para o domínio Vercel deste app. A collection usa esse mesmo domínio na variável `base_url`; para executar localmente, altere essa variável para `http://127.0.0.1:9083`. As APIs dos mocks não exigem token.

Execute as chamadas em ordem. Cada nova execução completa exige outro `run_id` (por exemplo, `manual-002`). Mantenha o mesmo valor ao repetir uma chamada ou recuperar timeout. A chave considera rota e bytes exatos do JSON: mudar espaços também pode gerar conflito. Para testar timeout no Postman, ajuste **Request timeout** em Settings; a collection não altera essa configuração automaticamente.

Na collection pronta, os IDs são gravados em variáveis da própria collection. Evite criar variáveis de ambiente com os mesmos nomes de IDs, pois elas podem prevalecer sobre os valores capturados.

## 1. Saúde

200. Endpoint público.

```bash
curl --location --request GET 'https://charging-two-mock.vercel.app/healthz' \
  --header 'Accept: application/json'
```

## 2. Capacidades

200. Capacidades do perfil.

```bash
curl --location --request GET 'https://charging-two-mock.vercel.app/v1/capabilities' \
  --header 'Accept: application/json'
```

## 3. Consultar conectores

200. connector-1 precisa estar AVAILABLE para iniciar.

```bash
curl --location --request GET 'https://charging-two-mock.vercel.app/v1/connectors' \
  --header 'Accept: application/json'
```

## 4. Solicitar início

202, PENDING. Copie id para session_id.

```bash
curl --location --request POST 'https://charging-two-mock.vercel.app/v1/sessions' \
  --header 'Accept: application/json' \
  --header 'Content-Type: application/json' \
  --header 'Idempotency-Key: {{run_id}}-start' \
  --data-raw '{"connector_id":"connector-1"}'
```

## 5. Consultar sessão

200. Acompanhe por esta rota durante todo o fluxo.

```bash
curl --location --request GET 'https://charging-two-mock.vercel.app/v1/sessions/{{session_id}}' \
  --header 'Accept: application/json'
```

## 6. Conector ocupado

409 connector_busy enquanto a primeira sessão não encerrou.

```bash
curl --location --request POST 'https://charging-two-mock.vercel.app/v1/sessions' \
  --header 'Accept: application/json' \
  --header 'Content-Type: application/json' \
  --header 'Idempotency-Key: {{run_id}}-busy' \
  --data-raw '{"connector_id":"connector-1"}'
```

## 7. Confirmar ativação

200, ACTIVE. Controle do simulador.

```bash
curl --location --request POST 'https://charging-two-mock.vercel.app/_mock/sessions/{{session_id}}/activate' \
  --header 'Accept: application/json' \
  --header 'Content-Type: application/json' \
  --header 'Idempotency-Key: {{run_id}}-activate' \
  --data-raw '{}'
```

## 8. Atualizar medição

200. Medição acumulada: 12000 Wh = 12 kWh.

```bash
curl --location --request POST 'https://charging-two-mock.vercel.app/_mock/sessions/{{session_id}}/meter' \
  --header 'Accept: application/json' \
  --header 'Content-Type: application/json' \
  --header 'Idempotency-Key: {{run_id}}-meter' \
  --data-raw '{"energy_wh":12000}'
```

## 9. Solicitar parada

200, STOP_REQUESTED.

```bash
curl --location --request POST 'https://charging-two-mock.vercel.app/v1/sessions/{{session_id}}/stop' \
  --header 'Accept: application/json' \
  --header 'Content-Type: application/json' \
  --header 'Idempotency-Key: {{run_id}}-stop' \
  --data-raw '{}'
```

## 10. Documento antes do fechamento

404 not_found antes de COMPLETED.

```bash
curl --location --request GET 'https://charging-two-mock.vercel.app/v1/sessions/{{session_id}}/receipt' \
  --header 'Accept: application/json'
```

## 11. Confirmar encerramento

200, COMPLETED. cost_cents=3000. O conector volta a ficar disponível.

```bash
curl --location --request POST 'https://charging-two-mock.vercel.app/_mock/sessions/{{session_id}}/complete' \
  --header 'Accept: application/json' \
  --header 'Content-Type: application/json' \
  --header 'Idempotency-Key: {{run_id}}-complete' \
  --data-raw '{}'
```

## 12. Consultar documento final

200. Documento simplificado com sessão e moeda BRL.

```bash
curl --location --request GET 'https://charging-two-mock.vercel.app/v1/sessions/{{session_id}}/receipt' \
  --header 'Accept: application/json'
```

## 13. Consultar eventos

404 poll_sessions_instead. Este operador usa consulta da sessão.

```bash
curl --location --request GET 'https://charging-two-mock.vercel.app/v1/events' \
  --header 'Accept: application/json'
```

## 14. Transição inválida

409 invalid_transition: sessão já completada.

```bash
curl --location --request POST 'https://charging-two-mock.vercel.app/_mock/sessions/{{session_id}}/activate' \
  --header 'Accept: application/json' \
  --header 'Content-Type: application/json' \
  --header 'Idempotency-Key: {{run_id}}-invalid-transition' \
  --data-raw '{}'
```

## 15. Rejeição do provedor

422 provider_rejected.

```bash
curl --location --request GET 'https://charging-two-mock.vercel.app/v1/capabilities' \
  --header 'Accept: application/json' \
  --header 'X-Mock-Scenario: reject'
```

## 16. Provedor indisponível

503 provider_unavailable.

```bash
curl --location --request GET 'https://charging-two-mock.vercel.app/v1/capabilities' \
  --header 'Accept: application/json' \
  --header 'X-Mock-Scenario: unavailable'
```

## 17. Atraso antes de processar

Espera três segundos. Configure Request timeout do Postman em 1000 ms para observar timeout, ou acima de 3000 ms para receber a resposta.

```bash
curl --location --request GET 'https://charging-two-mock.vercel.app/v1/capabilities' \
  --header 'Accept: application/json' \
  --header 'X-Mock-Scenario: timeout'
```

## 18. Timeout após efetivação

Execute após concluir o fluxo anterior. Configure Request timeout em 1000 ms; o estado pode ser efetivado mesmo sem resposta. Depois execute Recuperar comando incerto.

```bash
curl --location --request POST 'https://charging-two-mock.vercel.app/v1/sessions' \
  --header 'Accept: application/json' \
  --header 'Content-Type: application/json' \
  --header 'Idempotency-Key: {{run_id}}-uncertain' \
  --header 'X-Mock-Scenario: timeout-after-commit' \
  --data-raw '{"connector_id":"connector-1"}'
```

## 19. Recuperar comando incerto

Restaure Request timeout para 10000 ms. Mesma chave, rota e corpo: recupera resposta sem duplicar. Copie id para recovered_id.

```bash
curl --location --request POST 'https://charging-two-mock.vercel.app/v1/sessions' \
  --header 'Accept: application/json' \
  --header 'Content-Type: application/json' \
  --header 'Idempotency-Key: {{run_id}}-uncertain' \
  --data-raw '{"connector_id":"connector-1"}'
```

## 20. Consultar recurso recuperado

200. Consulta o resultado do comando incerto.

```bash
curl --location --request GET 'https://charging-two-mock.vercel.app/v1/sessions/{{recovered_id}}' \
  --header 'Accept: application/json'
```

## 21. Conflito de idempotência

409 idempotency_conflict: mesma chave, corpo diferente.

```bash
curl --location --request POST 'https://charging-two-mock.vercel.app/v1/sessions' \
  --header 'Accept: application/json' \
  --header 'Content-Type: application/json' \
  --header 'Idempotency-Key: {{run_id}}-uncertain' \
  --data-raw '{"amount_cents":1}'
```

## 22. Ativar sessão recuperada

200, ACTIVE. Limpeza do fluxo de timeout.

```bash
curl --location --request POST 'https://charging-two-mock.vercel.app/_mock/sessions/{{recovered_id}}/activate' \
  --header 'Accept: application/json' \
  --header 'Content-Type: application/json' \
  --header 'Idempotency-Key: {{run_id}}-recovered-activate' \
  --data-raw '{}'
```

## 23. Parar sessão recuperada

200, STOP_REQUESTED.

```bash
curl --location --request POST 'https://charging-two-mock.vercel.app/v1/sessions/{{recovered_id}}/stop' \
  --header 'Accept: application/json' \
  --header 'Content-Type: application/json' \
  --header 'Idempotency-Key: {{run_id}}-recovered-stop' \
  --data-raw '{}'
```

## 24. Concluir sessão recuperada

200, COMPLETED. Libera o conector para uma nova execução.

```bash
curl --location --request POST 'https://charging-two-mock.vercel.app/_mock/sessions/{{recovered_id}}/complete' \
  --header 'Accept: application/json' \
  --header 'Content-Type: application/json' \
  --header 'Idempotency-Key: {{run_id}}-recovered-complete' \
  --data-raw '{}'
```

## Observações

Os endpoints `/_mock` são controles de teste. Valores financeiros estão em centavos BRL e energia em Wh. Localmente sem Redis, reiniciar limpa os dados; com Redis, use outra chave de estado para um conjunto vazio. Uma resposta de autenticação da Vercel, em HTML, deve ser resolvida na proteção do deployment para alcançar a API pública do mock.
