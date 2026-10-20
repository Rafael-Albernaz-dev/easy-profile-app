# Remediação técnica — EasyProfileApp — 19/10/2026

## Parecer e fronteira de aceite

As três slices tratam os 16 achados da auditoria sobre a base `0445b967`, preservando as alterações locais preexistentes. Trabalho local, sem stage/commit/push, instalação, cutover, restart de serviços reais ou escrita de presets de produção.

**Health Score projetado: 95/100 para a remediação de engenharia local**, condicionado às provas e limites deste relatório. Esta projeção é uma autoavaliação do escopo corrigido, não uma nova auditoria independente nem certificação acústica. Homologação em GNOME Wayland, recursos com adaptadores reais e cutover/rollback reais continuam pendentes.

## Slice 1 — Instalação, cutover e rollback

### Engenharia

`install.sh` usa helpers pequenos para capturar estados systemd sem duplicar stdout. Estados transitórios/desconhecidos de guards são recusados. Um serviço EasyProfile já ativo também causa recusa antes de instalação, preservando o processo existente.

Dry-run não faz build, não cria diretórios e não escreve/substitui snapshots. O snapshot real é JSON validado, gravado em arquivo temporário com fsync e publicado sem sobrescrita por link atômico. Um backup anterior impede nova instalação até revisão explícita.

Depois de iniciar a desativação dos guards, falhas catcháveis e INT/TERM acionam rollback por trap. O health check tem prazo e verifica JSON: proteção confirmada, sem erro, controle habilitado, vínculos DSP e monitor gain confirmados. Um daemon de protocolo antigo não é aceito apenas por retornar status zero.

Rollback remove a ativação D-Bus antes de interromper o novo daemon, tenta restaurar ambos os guards, preserva estados positivos/negativos e runtime, verifica readback e os três hashes. Ausência/corrupção de backup não autoriza fallback cego. O snapshot não é apagado ao terminar.

### Significado no áudio

Uma falha na partida ou uma proteção parcial deixa de ser declarada como sucesso. Os scripts tentam devolver a proteção anterior exatamente ao estado registrado. Um guard originalmente parado não é ligado arbitrariamente. A remoção da ativação fecha o caminho instalado de reativação automática do substituto após rollback.

Nenhum script de cutover foi executado contra produção. Os testes exercitam os scripts reais com ferramentas falsas e HOME descartável em `/tmp`. Traps não podem executar após SIGKILL/perda de energia; nesses casos, o snapshot preservado serve para recuperação explícita.

### Provas e validação humana

`scripts/test-cutover.py` cobre falha de enable/start, saúde degradada, JSON inválido, status não zero, protocolo antigo, falha ao parar guard, estados negativos/runtime, dry-run sem escrita, backup preservado, sucesso seguido de rollback, divergência de hash em fixture falsa e recusa de substituir serviço ativo.

Falta validar comportamento real de systemd/D-Bus/GNOME numa sessão de cutover especificamente autorizada. Essa autorização não está implícita nos testes.

## Slice 2 — Sessão, privacidade, DSP e status

### Engenharia

Marcador de login e snapshot de áudio têm caminhos separados: `login_session.json` e `session.json`. A escrita compartilhada usa CreateTemp no diretório de destino, Sync, Rename e Sync do diretório, com permissões privadas e erros propagados.

Marcador inválido/ilegível ou identidade gráfica desconhecida não autoriza desmute. A permissão de login é registrada antes de qualquer desmute automático; falha na gravação interrompe a subida. Snapshot válido do mesmo login com marcador contraditório também é recusado. Uma tentativa de hardware que falha após consumir a permissão não provoca novo desmute automático no restart.

Flock em `$XDG_RUNTIME_DIR/easyprofile.lock` precede leitura da sessão e inicialização do hardware. A produção conecta o controlador a SessionStore com identidade gráfica e configuração calibrada; no mesmo login recupera níveis/presets de ambas as portas. Snapshot ausente ou inválido no restart não pode fabricar defaults válidos. Falhas de persistência permanecem visíveis no resultado/status.

Novos contratos usados pelos adaptadores leem o grafo e a seleção de dispositivos. Na subida e reparação, o daemon mantém default/configured sink virtual, saída EasyEffects na placa física e entrada no KA-1333 resolvido por USB/perfil. Repara monitor gain com níveis limitados e sink físico protegido. Background não autoriza desmute novo do mic; observações incompletas pedem mute de segurança e retornam degradação.

Cache reage a remoções/alterações, snapshots frescos, gerações e falhas de comandos por ID. Readbacks de volume/mute malformados são erros, jamais um snapshot implicitamente desmutado. A tolerância de volume aceita arredondamento de duas casas, sem esconder erro de um passo inteiro ou ultrapassagem do teto físico.

`GetStatus` lê uma string JSON publicada atomicamente. Operações lentas e fsync continuam serializados no worker, sem bloquear a leitura do snapshot. Cancelamento do contexto da inicialização chega aos comandos e esperas de rota.

### Significado no áudio

Reiniciar o daemon no mesmo login deixa de elevar saída/mic de 20%/30% para 95%/100%. Presets escolhidos por porta sobrevivem ao restart e vencem autoload. Um arquivo quebrado deixa de virar permissão para abrir o microfone.

Na futura substituição dos guards, apps devem continuar passando pelo sink virtual, filtros devem continuar ligados ao hardware correto e controles dos nós virtuais devem representar ganho efetivo de monitor. O controlador não altera o ganho físico USB nem os arquivos de preset.

O status rápido é o último snapshot consistente publicado, não uma nova inspeção síncrona de hardware em cada leitura. Não se afirma que toda operação real de volume/rota termine em 5 ms.

### Provas e validação humana

As cinco provas da auditoria viraram regressões: status durante worker bloqueado; restart com níveis/presets por porta; marcador corrompido; falha de commit do marcador; reconciliação de monitor gain. Casos adicionais cobrem USB ambíguo/incorreto, IDs reciclados, restauração da cadeia, reparação que falha, observe sem escrita de áudio, snapshot ausente/corrompido, I/O de persistência, cancelamento, flock, readbacks inválidos e 101 posições da conversão linear/cúbica.

Faltam hotplug e autoload reais, disponibilidade da identidade gráfica no ambiente systemd, escuta dos dois conectores e medição de watchers/subprocessos. Os adaptadores reais não foram liberados do bloqueio de testes para produzir essas provas.

## Slice 3 — Extensão, teclado e gates

### Engenharia

A fila de níveis usa canal/destino; rota, preset e Reset invalidam pendências e aguardam chamadas já enviadas. O novo método público `SetVolumeForDestination` valida o destino dentro do worker, protegendo também contra entrega atrasada. Chamadas CLI genéricas continuam compatíveis.

Sinais atrasados não apagam o valor otimista ainda pendente. Callbacks obsoletos não recriam atores; disable cancela chamadas, remove timers rastreados, bindings e sinais e libera referências da UI. Label de nível dá feedback imediato; ícones usam o estado publicado pelo daemon. Apenas `mic_privacy_muted` declara proteção confirmada nos dois alvos; falhas continuam visíveis como degradação. OSDs de rotina e helper sem uso foram removidos.

Restart deixou de existir no contrato/exportação D-Bus. O menu usa somente systemd, com tratamento de erro. Reset `*` não reinicia o processo. Schema XML e compilado são gerados juntos no empacotamento, incluindo `KP_Decimal` e `KP_Separator`.

README, arquitetura, RULES, handoff e governança privada refletem o contrato aprovado: +/- 1%; 1 AKG; 2 SP166; 3 foco mic; 4/5/6 presets; 0 dual-mute; decimal mute saída; Enter pânico; / placeholder; * Reset; 7/8/9 no-op; NumLock modo áudio. Observação exige `--observe` explícito. Homologação antiga é identificada como histórica.

### Significado no áudio

Um ajuste iniciado no fone não é reenviado à caixa após trocar de destino. Reset não recebe um nível antigo pendente logo depois de restaurar a base diária. O mic não é anunciado como protegido apenas por otimismo visual. DynamicEqualizer continua sem alterar o DSP.

### Provas e validação humana

Teste dos métodos reais da extensão contra atores/proxy em memória cobre barreiras, sinais atrasados, falhas, callbacks após disable, limpeza de timers, NumLock e repetição. Integração usa o proxy Gio real no bus privado e cancela uma chamada pendente. Cancelar o cliente não desfaz uma operação já aceita pelo servidor; as barreiras tratam esse limite.

A UI não foi instalada/carregada no Shell real nesta entrega. O roteiro humano está no [handoff atualizado](../handoffs/EP-004-EP-005-HANDOFF.md).

## Rastreabilidade dos 16 achados

| Achado | Correção principal | Prova principal |
|---|---|---|
| 1 | Trap e saúde estruturada com prazo | Falhas de partida e saúde em test-cutover |
| 2 | Dry-run sem escrita, JSON validado/atômico, estados simétricos | Backup preservado, estados negativos/runtime e hash falso divergente |
| 3 | Remoção da ativação e parada verificada do daemon | Ausência do arquivo e unidade fake inativa após rollback |
| 4 | Default sink e vínculos de dispositivos DSP | Plano puro de reparação + reconciliação com fakes |
| 5 | Marcador fail-safe e gravação durável | Corrupção, identidade desconhecida e falha de commit |
| 6 | Flock pré-hardware | Exclusividade unitária e segundo processo fake recusado |
| 7 | Cache por geração/identidade, eventos e erros | Snapshot com ID reciclado/ausente e monitor alterado |
| 8 | Auditoria/reparação de monitor gain e flags de confirmação | Reconcile recupera ganho; erro mantém hardware mutado |
| 9 | Persistência por login/destino/preset | Restart 20%/30%, preset alternativo e outra porta 40% |
| 10 | Fila por destino, barreiras e validação dentro do daemon | UI simulada + requisição de destino obsoleto recusada |
| 11 | `--observe` explícito em todos os roteiros atuais | Revisão documental e teste observe sem reparação |
| 12 | Snapshot de status publicado atomicamente | Leitura durante worker bloqueado com orçamento de 5 ms |
| 13 | Restart remoto removido; systemd único | Introspecção e chamada remota de Restart recusada |
| 14 | Mapa único das 17 teclas | Schema, documentação e teste de bindings/flags |
| 15 | Timers/cancelamento/atores e feedback pendente | Teste de lifecycle e cancelamento Gio real |
| 16 | AST, regras honestas, bus sem ativação e budgets medidos | Seis gates, documentos e integridade privada no Git |

## Resultado final e integridade

**Aceite automatizado local: 6/6 gates aprovados, saída 0**, com a árvore de código final e todas as regressões novas. Comando: `EASYPROFILE_TEST_MODE=1 GSETTINGS_BACKEND=memory make quality-gate`. A suíte Go com race passou; a integração foi executada separadamente em D-Bus privado. Não houve data race nos caminhos executados.

| Medição | Resultado final | Escopo |
|---|---:|---|
| Parse do fixture capturado | 3.695 ms/op | Orçamento <5 ms |
| GetStatus com hardware bloqueado | 186ns/op | Média de 100 chamadas Go diretas, sem transporte; orçamento <5 ms |
| GetStatus D-Bus | 647.611µs | Média de 100 chamadas, fake/race |
| CLI status p50 / p95 / máximo | 4.097 / 6.068 / 13.931 ms | 100 processos contra daemon fake; orçamento p95 <15 ms |
| RSS durante 60 s | 8508 → 8508 KiB | Daemon fake sem race |
| RSS após chamadas CLI | 9432 KiB | Abaixo de 15 MB |
| CPU ociosa por 60 s | 0.0% de um núcleo | Nenhum incremento de ticks; resolução do contador, sem watchers reais |

**Integridade anterior/posterior:** ambos os guards permanecem `active`; os três presets têm SHA-256 idênticos à baseline. Governança privada continua ignorada e não rastreada; nenhum arquivo foi colocado em stage. O teste CLI preexistente foi preservado byte a byte. Não houve commit/push, instalação ou mudança no áudio de produção.

- [Log dos seis gates](../evidence/remediation-2026-10-19-quality-gate.log).
- [Manifesto de integridade e métricas](../evidence/remediation-2026-10-19-integrity.json).
- [Roteiro de homologação humana](../handoffs/EP-004-EP-005-HANDOFF.md#6-roteiro-humano-futuro--não-executado-nesta-remediação).

Esses resultados encerram a remediação automatizada local. Provas acústicas, recursos reais, instalação da UI no Shell e cutover/rollback físicos permanecem no roteiro acompanhado; não foram substituídos por simulação.
