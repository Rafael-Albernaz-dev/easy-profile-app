# Gate de impacto operacional

O piso é **75%**, com meta de **80%**. Trata-se de uma pontuação auditável de
recursos e impacto observado nos serviços; não significa “75% mais rápido”,
qualidade acústica, cobertura de testes ou aprovação de uma chamada. O gate real
recusa substituir medidas por resultados de fakes. Os orçamentos anteriores do
parser, D-Bus e daemon fake continuam independentes em `RULES.md`.

Resultado mais recente, em **20/10/2026**: medição real realizada, **score não
certificado**. A janela Boost ON teve novos incrementos de ERR; a janela OFF
terminou sem reprodução, invalidando a equivalência de carga. Os resultados e
as falhas foram preservados na seção de auditoria real abaixo.

## Medição passiva e comparação

`scripts/measure-service-impact.py` lê `/proc` e cgroups v2 dos processos do
usuário: EasyProfile, EasyEffects, PipeWire, pipewire-pulse, WirePlumber e GNOME
Shell. Não executa `wpctl`, `pw-cli`, `amixer`, `easyeffects`, reparações,
instalação, reinícios, abertura de chamada ou mudanças de microfone. Recusa
`EASYPROFILE_TEST_MODE`, para não confundir coleta real com testes herméticos.

CPU é contabilizada no cgroup, incluindo os subprocessos que terminam entre
amostras. Memória é `memory.current` do cgroup, incluindo filhos e memória
contabilizada pelo kernel; **não é RSS individual**. Tarefas incluem threads.
Os picos são amostrados, normalmente a cada 250 ms, e podem perder picos mais
curtos. O coletor não conta exatamente criações de subprocessos, frequência de
reconciliação nem XRUNs; não inventa essas medidas a partir das amostras.
Novas coletas também registram a CPU do próprio observador, que não deve ser
atribuída ao daemon. Na baseline inicial abaixo esse campo ainda não existia.

Para cada feature, medir um par de pelo menos 60 s com o **mesmo binário já
instalado**, mesma sessão, processos e carga de áudio/aplicações:

```bash
# O operador prepara o cenário acompanhado; estes comandos só observam.
python3 scripts/measure-service-impact.py \
  --scenario baseline --workload-id musica-e-chamada-01 \
  --output /tmp/ep-off.json --notes 'Feature desligada; carga controlada'

# Após o operador ativar UMA feature no mesmo cenário:
python3 scripts/measure-service-impact.py \
  --scenario boost --workload-id musica-e-chamada-01 \
  --output /tmp/ep-on.json --notes 'Boost ativo; mesma carga'

python3 scripts/performance-impact-gate.py /tmp/ep-off.json \
  --candidate /tmp/ep-on.json --validation /tmp/ep-validacao.json \
  --json-output /tmp/ep-score.json
```

`--scenario` também aceita `sharing` e `push-to-talk`. É uma declaração do
operador, não prova de ativação. Comparar separadamente reprodução contínua,
chamada, PTT sustentado e rajadas de teclas/preset/saída. Para avaliar a própria
instalação, repetir baseline e candidato instalados em sessões acompanhadas;
não comparar binários diferentes e chamar isso de custo de uma feature.

O gate exige identidade de processos/binários/cgroups, mesma carga identificada,
intervalos iguais e CPU global com variação máxima de 15 pontos percentuais.
Também exige quatro arquivos de status reais, início/fim de OFF e ON. Compara
nível, preset, destino e os três mutes independentes; confirma o modo esperado e,
para Boost com reprodução contínua, DSP observado e ausência de estado idle.
Um identificador de carga e um booleano do operador não substituem esses dados.
Uma carga rotulada igual ainda pode mudar: a confirmação humana da carga
controlada também é necessária. Uma comparação isolada não estabelece
causalidade. Recomenda-se três pares alternados off/on e preservar todos os
resultados, sem escolher apenas o melhor.

## Critérios e cálculo

Os limites reais abaixo são **provisórios e explícitos**, para a homologação
local neste hardware. Não foram derivados de uma medição acústica aceita.
A meta por critério recebe o peso completo. Entre meta e teto, os pontos caem
linearmente: `peso × (teto − medido) / (teto − meta)`. A soma tem 100 pontos.
Reduções de consumo em um vizinho não compensam aumentos em outro.

| Critério | Peso | Meta por critério | Teto operacional |
|---|---:|---:|---:|
| CPU média EasyProfile e filhos | 25 | ≤1% de um núcleo | <5% |
| Pico CPU EasyProfile e filhos, amostrado | 10 | ≤25% de um núcleo | <75% |
| Soma dos aumentos de CPU dos cinco vizinhos | 25 | ≤2 pontos percentuais de um núcleo | <10 pp |
| Pico de memória do cgroup EasyProfile | 15 | ≤32 MiB | <48 MiB |
| Pico de tarefas do cgroup EasyProfile | 5 | ≤32 | <96 |
| Aumento da pressão CPU global (`/proc/pressure/cpu`) | 20 | ≤0,5 ponto percentual | <3 pp |

Memória/tarefas mantêm margem diante de `MemoryMax=64M` e `TasksMax=128` da
unidade. CPU não recebe quota apertada: throttling pode atrasar justamente mute
e troca de rota. O orçamento já aprovado de CPU ociosa **<1% por 60 s** continua
obrigatório quando a baseline é declarada ociosa. As demais metas de `RULES.md`
não são substituídas pelo score.

`TARGET_MET`: score ≥80; `FLOOR_MET`: 75≤score<80; `FAIL`: score<75.
`NOT_CERTIFIED`: faltam evidências comparáveis ou existe bloqueador. O comando
retorna zero somente nos dois primeiros casos, e retorna um nos dois últimos.
Dados inválidos retornam erro de uso (dois). É possível gerar apenas a tabela
da baseline, mas isso retorna um e deixa o score vazio.

## Bloqueadores que pontos não compensam

- Ausência de baseline/feature comparáveis ou de evidência acompanhada de
  ativação, transporte/DSP, restauração e mutes independentes.
- Mudança de preset calibrado, XRUN observado, stutter audível ou saúde acústica
  desconhecida. Ausência de contador não é zero XRUN.
- Troca de PID/binário/cgroup entre janelas, evento de limite de memória/OOM ou
  throttling em qualquer serviço observado.
- Quota CPU finita em um cgroup ancestral, pois seu throttling exige medição
  adicional além dos contadores do cgroup folha.
- Qualquer teto operacional atingido, contexto de CPU incompatível, ou violação
  da CPU ociosa quando o cenário foi declarado idle.

Arquivo de validação acompanhado (preencher com evidências reais; este exemplo
tem campos nulos e **não passa**):

```json
{
  "source": "accompanied-live",
  "baseline_capture_id": "ID da coleta off",
  "candidate_capture_id": "ID da coleta on",
  "workload_id": "musica-e-chamada-01",
  "baseline_idle": false,
  "workload_controlled": null,
  "feature_off_baseline_confirmed": null,
  "feature_on_candidate_confirmed": null,
  "presets_identical": null,
  "independent_mutes_preserved": null,
  "restoration_verified": null,
  "transport_or_dsp_confirmed": null,
  "baseline_xruns": null,
  "candidate_xruns": null,
  "candidate_audible_stutter": null,
  "audio_status_files": {
    "baseline_start": "off-inicio-status.json",
    "baseline_end": "off-fim-status.json",
    "candidate_start": "on-inicio-status.json",
    "candidate_end": "on-fim-status.json"
  },
  "evidence_references": []
}
```

`evidence_references` deve apontar para registros verificáveis: hashes antes e
depois, contadores PipeWire/XRUN com método e intervalo, topologia/readbacks e
resultado humano da chamada. O script valida presença/consistência dos campos,
mas não autentica relatos nem interpreta o conteúdo dessas referências. A revisão
das provas continua necessária. “Há um arquivo LV2” não comprova DSP, e status
do daemon sozinho não comprova que o interlocutor ouviu áudio.

Os caminhos de status relativos são resolvidos em relação ao arquivo de
validação; o gate grava seus SHA-256 no resultado. Não aceita apenas dicionários
de status embutidos pela CLI. Os dicionários dos testes são exclusivamente
fixtures em memória. Não atribui pontos provisórios a um par bloqueado.

`baseline_xruns` e `candidate_xruns` referem-se a eventos novos observados nas
respectivas janelas, não aos totais históricos. `pw-top` expõe **ERR = total de
XRUNs e erros**, sem identificar eventos únicos. Descartar o quadro inicial
`Creating` com zeros; usar quadros inicializados, identidades/seriais comparáveis
e diferenças antes/depois. Se um contador cair, zerar ou um nó desaparecer, o
delta fica desconhecido; isso não é melhora nem zero XRUN. Incrementos ERR podem
ser registrados em `audio_error_increments`, por janela e nó; o gate bloqueia
valores positivos mesmo sem conhecer quantos XRUNs distintos ocorreram.

## Auditoria inicial de 19/10/2026

Coleta passiva às 23:45:47 BRT, 60,00 s, 236 amostras; daemon instalado PID
1712712. Desenvolvimento e navegador estavam em uso. Estados das features não
foram controlados. Portanto, esta é uma baseline descritiva, **não uma baseline
idle controlada nem uma comparação off/on**. Captura local:
`/tmp/easyprofile-impact-baseline-20261019.json`.

| Serviço | CPU média, um núcleo | Pico CPU amostrado | Memória pico do cgroup | Feature / impacto |
|---|---:|---:|---:|---|
| EasyProfile + filhos | 0,37% | 69,74% | 11,78 MiB | Pendente |
| EasyEffects | 5,38% | 10,73% | 75,98 MiB | Pendente |
| PipeWire | 0,61% | 6,46% | 33,07 MiB | Pendente |
| pipewire-pulse | 0,53% | 3,37% | 37,48 MiB | Pendente |
| WirePlumber | 0,02% | 3,77% | 13,14 MiB | Pendente |
| GNOME Shell completo | 28,56% | 48,43% | 361,48 MiB | Pendente |

CPU global ocupada: 55,88%; pressão CPU: 21,44%; load médio de um minuto caiu
de 6,36 para 5,14. Nenhum evento de memória/OOM ou throttling nos seis cgroups
durante a janela. O pico curto do daemon merece comparação por cenário; sua
média baixa não elimina a possibilidade de interferência em transientes. O custo
da extensão não pode ser separado do GNOME inteiro nesta captura.

A unidade `easyeffects.service` está inativa, mas o processo EasyEffects está
ativo em um scope de aplicação GNOME: consultar apenas a unidade teria omitido
seu consumo. EasyProfile está ativo, com `MemoryMax=64M`, `TasksMax=128` e sem
CPUQuota. Nenhum limite ou serviço foi alterado pela auditoria.

**Score real: não certificado.** Falta par controlado da versão candidata e
prova acompanhada de transporte/DSP, saúde acústica e restauração. Estes dados
não confirmam nem descartam a origem do stuttering relatado.

## Provas de engenharia e próximos testes

```bash
python3 scripts/test-performance-impact.py
EASYPROFILE_TEST_MODE=1 ./scripts/performance-check.sh
```

O primeiro usa apenas dicionários em memória e verifica inclusive score exato
75/80, rejeição de fakes, dados ausentes/não finitos, trocas de processos,
throttling/OOM e bloqueios acústicos. Não lê serviços reais. O segundo mede
somente daemon fake em D-Bus privado e continua sujeito a seus orçamentos.
Um resultado fake não preenche nenhuma linha operacional pendente.

Execução serial em 19/10/2026, sem builds/testes concorrentes do agente principal:

| Prova de engenharia | Medido | Orçamento | Resultado |
|---|---:|---:|---|
| Parser de dump capturado | 3,580 ms/op | <5 ms/op | Passou |
| CLI + D-Bus + daemon fake, 100 amostras | p95 5,20 ms; máximo 5,43 ms | p95 <15 ms | Passou |
| RSS máximo observado do daemon fake | 9.896 KiB (10,13 MB) | <15 MB | Passou |
| CPU ociosa fake, 60,000 s | 0,00% de um núcleo na resolução dos ticks | <1% | Passou |
| Regressões do score, em memória | 14 casos | Todos aprovados | Passou |
| Impacto operacional das features / DSP / chamada | Sem par acompanhado | Score ≥75; meta ≥80 | Não certificado |

Logs locais ignorados pelo Git: `docs/evidence/fake-performance-2026-10-19.log`
e `docs/evidence/parser-performance-2026-10-19.log`. A carga restante da máquina
não foi controlada; a execução serial evita a competição deliberada entre os
testes desta tarefa, sem prometer isolamento do sistema inteiro.

Há 100 ms de agrupamento dos eventos de grafo/dconf e um worker serial.
Reconciliação saudável já tem regressão para evitar inspeções duplicadas. Isso
limita trabalho redundante, mas não mede a taxa real sob tempestade de eventos.
Boost acrescenta DSP no processo EasyEffects; compartilhamento acrescenta um
loopback; PTT usa renovação D-Bus enquanto a tecla é segurada. Seus custos reais,
subprocessos de transição e eventual perda de prazo precisam ser comparados nas
janelas acompanhadas. O coletor não executa essas transições automaticamente.

O gate legado ainda exige guards ativos e pode falhar após o cutover. Este gate
adicional não reativa guards, não altera presets e não converte aquela falha em
verde. Resultado global deve continuar reportando esse bloqueio separadamente.

## Auditoria real acompanhada — 20/10/2026

Binário definitivo instalado e em execução: SHA-256
`e8301571aa05266606caa93f73f62eea555735b82e105289e33b246f3a301d70`, PID
1981037; extensão v2 carregada após novo login. As duas janelas abaixo mantiveram
as identidades dos seis processos. Esta etapa já usou a instalação nova;
os relatos anteriores de “não instalado” são históricos.

A primeira coleta OFF começou com status de 60%; o status posterior já mostrava
45% **antes de ligar Boost**. O usuário confirmou que reduziu manualmente.
Essa amostra foi preservada e excluída da comparação controlada. Um coletor
inicialmente lançado após o pré-check rejeitar a diferença foi encerrado sem
completar arquivo; áudio e serviços não foram alterados pelo coletor.

Foi então realizado um par **ON → OFF**, com 60 s cada e volume declarado estável
em 45%, preset AKG K52 — Padrão, sem testes/builds concorrentes. Cronologia:
ON às 00:14:24 BRT; OFF às 00:17:51 BRT. Não houve ganho acima de 100% nesta prova.

| Serviço | CPU OFF¹ | CPU ON¹ | Δ observado ON−OFF² | Memória OFF → ON³ |
|---|---:|---:|---:|---:|
| EasyProfile e filhos | 0,599% | 0,00004% | −0,599 pp | 13,04 → 10,27 MiB |
| EasyEffects | 1,040% | 2,089% | +1,049 pp | 36,34 → 36,17 MiB |
| PipeWire | 0,358% | 0,570% | +0,212 pp | 31,36 → 31,06 MiB |
| pipewire-pulse | 0,290% | 0,513% | +0,223 pp | 37,48 → 37,48 MiB |
| WirePlumber | 0,0278% | 0,000% | −0,0278 pp | 13,67 → 13,17 MiB |
| GNOME Shell completo | 11,114% | 18,436% | +7,322 pp | 362,16 → 326,53 MiB |

¹ Percentual de um núcleo. ² **Deltas descritivos de cargas que terminaram
diferentes; não medem o impacto causal de Boost.** ³ Pico amostrado do cgroup,
não RSS individual. A reprodução OFF terminou durante a janela: seu estado final
ficou `output_dsp_idle=true`, sem confirmação DSP ativa, e o nó de reprodução
Chrome desapareceu. O menor consumo OFF não pode ser usado como baseline de
reprodução equivalente. O custo do GNOME inteiro não isola a extensão.

CPU global ocupada: OFF 23,56%, ON 33,14%; pressão CPU: OFF 7,02%, ON 9,80%.
O próprio observador consumiu OFF 1,72% e ON 1,82% de um núcleo, registrado
separadamente. Não houve eventos de OOM/limite de memória ou throttling nos
cgroups observados. O daemon OFF teve pico amostrado de 45,33%, numa janela com
mudança do grafo no fim; o ON em regime teve pico de 0,01%. Nenhum desses picos
representa o custo da transição manual de ativação, que ocorreu antes da coleta.

### Transporte e saúde de áudio observados

Nos snapshots inicial e final ON havia caminho **ativo**:
`easyeffects_sink → equalizer → limiter → spectrum → output_level → saída física`.
Os nós e seriais permaneceram iguais. Isso prova transporte pelo limitador nos
dois snapshots; não apenas configuração GSettings ou presença de arquivo LV2.

| Contador / observação | ON, antes → depois | OFF, antes → depois | Interpretação |
|---|---:|---:|---|
| ERR da saída física, ID 40 | 14 → 15 | 15 → 0, estado suspended | +1 no ON; reset no OFF |
| ERR do sink EasyEffects, ID 83 | 0 → 1 | 1 → 0, estado suspended | +1 no ON; reset no OFF |
| ERR do Chrome, ID 58 | 18 → 18 | 18 → nó ausente | Sem aumento observado ON; OFF incomparável |
| Relato humano na ativação | Mute por aproximadamente 1 s | — | Corte real de transição, fora da janela em regime |
| Relato humano em regime / ao desligar | “Sem engasgos contínuos” | “desligou e o som continuou” | Aceite humano limitado a esta etapa a 45% |

Os dois incrementos de ERR podem refletir propagação de um mesmo evento; não se
afirmam “dois XRUNs distintos”, nem se atribui a causa exclusivamente ao Boost.
Contadores históricos já eram não zero antes. A queda dos contadores OFF também
não apaga a falha ON. Nenhuma terceira rodada foi solicitada para substituir os
resultados desfavoráveis.

**Gate real: `NOT_CERTIFIED`, saída 1, score vazio.** Bloqueios: reprodução OFF
interrompida/idle, contador OFF reiniciado, saúde por XRUN desconhecida e novos
incrementos ERR no ON. O piso 75% e a meta 80% permanecem; não foi calculada uma
porcentagem sobre dados incompatíveis.

### Engenharia, integridade e limites restantes

O gate foi endurecido após a coleta, sem mudar pesos ou tetos: exige os quatro
status, verifica deriva de nível/preset/destino/mutes e recusa idle como prova de
DSP ativo. **23 regressões em memória passaram**, incluindo deriva 60→45,
DSP desconhecido, modos errados, ERR positivo e reset de contador. A execução
real do gate rejeitou este par conforme os bloqueios acima. Testes em memória
não homologam DSP.

Os 15 arquivos de presets conservaram os hashes antes/depois. Ao final da coleta,
volume 45%, preset, destino e três mutes independentes foram preservados; Boost
ficou OFF. `systemctl --user is-active` confirmou EasyProfile ativo e ambos os
guards legados inativos; não foram reativados para satisfazer o gate histórico.
A instalação e o controle humano ocorreram no fluxo principal autorizado; este
subagente só leu serviços/áudio e executou coletores passivos, sem alterar rotas,
mutes, volume, presets, binário, extensão ou estado de serviços.

Evidências locais, ignoradas pelo Git:
`docs/evidence/boost-impact-live-2026-10-20/`. Contêm todas as coletas originais,
quatro status usados, topologias, quadros `pw-top`, hashes, relatos humanos,
`pair45-transport-and-counters.json`, `validation45.json`, `gate45.json` e
`gate45.md`. Para reproduzir o relatório congelado:

```bash
python3 scripts/performance-impact-gate.py \
  docs/evidence/boost-impact-live-2026-10-20/baseline45-impact.json \
  --candidate docs/evidence/boost-impact-live-2026-10-20/candidate45-impact.json \
  --validation docs/evidence/boost-impact-live-2026-10-20/validation45.json
```

Permanecem fora desta prova: Boost acima de 100% até 150%, desligamento/clamp
nesses níveis, rajadas de teclas, troca de saída/preset, PTT, compartilhamento e
recepção em chamada. O corte de ativação de aproximadamente 1 s também não foi
homologado como experiência aceitável. Uma medição futura de impacto precisa de
reprodução contínua nas duas janelas e investigação dos incrementos ERR.
