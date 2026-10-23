# charging-two-mock

API REST Go independente para simular o gateway charging-rest da ElecPay. Contrato experimental, sem compatibilidade homologada com fornecedores reais. Configurado para Vercel com preset Go e `cmd/api/main.go`. Sem dependências Go externas.

## Local

Requer Go 1.22 ou superior e make. O processo lê variáveis de ambiente; não carrega `.env` automaticamente.

```sh
export MOCK_API_TOKEN=local-test-token
make run
# outro terminal:
curl http://127.0.0.1:9083/healthz
curl http://127.0.0.1:9083/v1/capabilities -H 'Authorization: Bearer local-test-token'
make check
```

Sem Redis, o estado fica em memória e é descartado ao reiniciar. `PORT` altera a porta. `MOCK_API_TOKEN` é obrigatório. Os exemplos completos de rotas, idempotência, transições e cenários de falha estão em [Contrato REST](docs/rest-contract.md); use somente as rotas do perfil deste projeto. Todos os endpoints recebem/devolvem JSON.

## Vercel

1. Importe este repositório como um projeto independente. Root Directory: raiz do repositório. O `vercel.json` define framework `go` e build do servidor.
2. Conecte um banco Upstash Redis via Marketplace ou configure um existente. Não há provisionamento automático neste repositório.
3. Configure `MOCK_API_TOKEN` com um segredo de teste próprio, `UPSTASH_REDIS_REST_URL`, `UPSTASH_REDIS_REST_TOKEN` e `MOCK_STATE_KEY`. Use chave diferente por serviço e ambiente, por exemplo `charging-two-mock:preview:state:v1` e `charging-two-mock:production:state:v1`. Para previews simultâneos isolados, use uma chave por branch/deployment ou bancos separados.
4. Faça deploy via integração Git ou execute `vercel` na raiz para preview. Configure os valores também no ambiente Preview. A configuração recusa iniciar na Vercel sem Redis ou sem chave de estado explícita.
5. Valide `/healthz`, `/v1/capabilities` e um fluxo com escrita/leitura. `/healthz` é liveness e não consulta Redis; uma operação autenticada detecta indisponibilidade do armazenamento.

O projeto está preparado para deploy; nenhum deploy é executado por estes arquivos. O runtime Go da Vercel está em beta na documentação consultada em 22/10/2026.

## Estrutura e persistência

- `cmd/api`: servidor HTTP, configuração e encerramento gracioso.
- `internal/app`: autenticação, composição e execução com armazenamento persistente.
- `internal/simulator`: estados e operações do mock.
- `internal/storage`: cliente Upstash REST e compare-and-swap atômico via Lua.
- `docs/rest-contract.md`: contrato e exemplos.

Com Redis, cada requisição carrega o estado e cada POST bem-sucedido confirma um snapshot por compare-and-swap. Conflitos reexecutam até oito vezes; resultado incerto retorna 503 e deve ser recuperado repetindo a mesma chave de idempotência. Saldo, reservas e idempotência são confirmados juntos, compartilhados entre instâncias. GET não grava. A falha simulada após commit atrasa a resposta somente depois da gravação Redis.

Esse armazenamento serializa o estado completo: é adequado a mocks pequenos, com limite de 2 MiB por snapshot. Não há TTL, expurgo, paginação ou isolamento por usuário. Para limpar um ambiente, escolha uma nova `MOCK_STATE_KEY`; não há endpoint destrutivo. Controles `/_mock` usam o mesmo token e são exclusivos de testes. Os serviços não executam operações financeiras reais nem enviam webhooks.

## Verificação

`make check` executa testes com detector de race, build, vet e formatação. Os testes de armazenamento usam servidor HTTP Redis simulado, sem credenciais externas. A compilação local não equivale a build ou homologação remota na Vercel/Upstash.

Referências: [Vercel Go](https://vercel.com/docs/functions/runtimes/go), [Upstash REST](https://upstash.com/docs/redis/features/restapi).

## Exemplos de curl

Veja os [curls para importar no Postman](docs/curl-examples.md), com uma chamada por bloco, variáveis, respostas esperadas e cenários de falha. Ou importe a [collection pronta](postman/collection.json), que captura os IDs automaticamente.
