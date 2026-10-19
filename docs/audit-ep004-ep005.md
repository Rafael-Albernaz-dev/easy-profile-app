# Parecer de auditoria independente — EP-004 + EP-005

Data: 18/10/2026. Base: `main`, HEAD `647f5bc`, alterações locais sem commit.

## Parecer

**Aceite técnico local condicionado à validação humana do desktop.** A revisão final não identificou bloqueadores P0/P1 pendentes nos fluxos examinados. O auditor executou o quality gate independentemente e obteve **6/6 portões aprovados, exit 0**. Após os ajustes finais de ordem de segurança e empacotamento, reexaminou o código, o ZIP e o log final do executor `docs/evidence/quality-gate.log`, novamente com **6/6 portões aprovados**. Não houve builds em paralelo nessa última conferência.

Este parecer aprova a engenharia e as provas automatizadas locais descritas abaixo. Não constitui homologação auditiva, validação da extensão dentro do GNOME Shell, liberação de instalação/cutover dos guards ou autorização de commit. O aceite integral de uso real continua pendente do roteiro humano; a política de login e o cutover pertencem à EP-006.

## Escopo e independência

O auditor leu a governança na ordem solicitada: `AGENTS.md`, `.agents/easyprofile/CONTEXT.md`, `DECISIONS.md`, `ROADMAP.md`, `RULES.md` e `ARCHITECTURE_AND_REQUIREMENTS.md`. Revisou `internal/domain/`, `internal/adapters/`, `internal/daemon/`, `cmd/easyprofilectl/`, `gnome-extension/`, o empacotamento da extensão e os scripts de qualidade/integração.

A revisão de código e a execução de ferramentas foram independentes da implementação. O executor recebeu os achados e implementou as correções; o auditor não modificou código funcional. A única escrita documental do auditor é este parecer. Não houve stage, commit, push, instalação de ferramentas/extensão ou invocação mutável do áudio real pelo auditor.

## Engenharia e contratos examinados

A arquitetura mantém um único IPC, Session D-Bus, com exportação explícita dos métodos públicos e introspecção compatível. O domínio conserva o snapshot de três booleans e a memória por destino. O daemon serializa comandos, prioriza pânico, cancela trabalho normal em curso, aceita emergência mesmo com a fila normal cheia e agrega passos de volume adjacentes sem atravessar barreiras de rota/mute/pânico.

Os adaptadores reais usam argumentos explícitos e contextos com prazo; recusam execução quando a variável `EASYPROFILE_TEST_MODE` está presente. A suíte usa fakes e o teste de integração cria um barramento privado, `GSETTINGS_BACKEND=memory`, diretórios temporários e sentinelas no PATH para `wpctl`, `pw-cli`, `pw-dump`, `amixer`, `easyeffects`, `gsettings`, `pactl` e `pw-metadata`.

O exame de concorrência incluiu locks, filas, canais de resposta, encerramento do worker, watchers de subprocessos e callbacks da extensão. O código revisado cancela e recolhe processos observadores, interrompe timers, fecha conexões próprias da CLI e invalida callbacks após disable ou mudança do proprietário D-Bus. Os testes com `-race` não detectaram races nos caminhos executados; isso não prova ausência de races em todos os caminhos possíveis.

A configuração de hardware/calibração foi separada em `config.example.json`, com validação de destinos, pinos opostos, limites e identificação da rota inicial pelos nomes configurados. O daemon de desenvolvimento usa memória e adota mutes observados ao iniciar, evitando reaplicar uma política automática de desmute de login ainda não homologada.

### Achados corrigidos e reexaminados

| Prioridade inicial | Achado | Resultado da revisão final |
|---|---|---|
| P0 | Erros de mute/hardware descartados, com sucesso aparente de privacidade | Erros propagados; estados observado/desejado e degradação explícitos; readback dos alvos |
| P0 | Mic físico passado como prefixo a um resolvedor de igualdade | Prefixo configurável e resolução sem cache de ID obsoleto; ambiguidade rejeitada |
| P0 | Pânico/reset colapsavam os mutes independentes do mic | Aplicação separada dos estados virtual/físico; regressão cobre divergência e override |
| P0 | Pânico bloqueado por fila cheia ou falha de inspeção não essencial | Prioridade aceita; snapshot paralelo de três alvos com prazo curto; quatro tentativas de proteção independentes; snapshot desconhecido bloqueia restore automático |
| P0 | Runner legado podia enviar comandos ao daemon real do usuário | Wrapper encaminha ao harness que sempre cria barramento privado; clientes de teste recusam barramento fora do harness |
| P1 | SPA Props incorreto e conversão de volume duplicada/incorreta | Params `monitor.channel-volumes` e readback; contrato linear na porta, conversão cúbica única no adaptador; regressão 50% → ganho linear 0,125 |
| P1 | Troca de rota sem mute físico confirmado e com erros suprimidos | Sink físico protegido, pinos isolados/reafirmados, rota/DSP/níveis confirmados antes da liberação; falhas mantêm proteção física |
| P1 | Preset declarado apenas por metadado, bypass/erros ignorados | Carregamento real sem fallback enganoso, validação de direção/arquivo/cadeia e confirmação do preset, plugins e bypass |
| P1 | UI sem reconexão segura e OSD antecipando sucesso | Observação de owner, cancelamento/geração, conclusão de callbacks em falha e OSD após resposta/status |
| P1 | Metadata sem ligação do schema usado por `getSettings()` e módulo extra ausente do pack padrão | `settings-schema` corrigido; gate confere XML/versão Shell 46; pack inclui `dbus.js` explicitamente e verifica entradas do ZIP |
| P1 | Habilitação de monitor poderia efetivar ganho herdado acima de 100% antes da proteção inicial | Sink físico e pinos protegidos antes de Auto-Mute/monitor; níveis limitados antes de habilitar monitor; regressão específica com 105% aprovada |
| P2 | Lista de presets da rota anterior e estado observado desatualizado | Cache filtrado por destino e atualização dos estados observados na reconciliação |
| P2 | Persistência perdia mic em 0% e aceitava preset de outra rota | Restauração validada com regressões específicas |
| P2 | Gate podia ignorar staticcheck e integração terminava com SIGPIPE | Linter obrigatório; captura da saída antes de procurar texto; gate final aprovado |

## Tradução para o áudio

O volume exibido ao usuário é uma porcentagem perceptual; o ganho interno linear é o cubo dessa fração. Manter a conversão em uma única fronteira evita que 50% acabe aplicado como aproximadamente 79%. O ajuste pós-DSP do mic altera o resultado processado, preservando o ganho físico USB calibrado.

`monitor.channel-volumes` habilita o ponto de atenuação dos nós virtuais do EasyEffects. A inspeção passiva em `docs/evidence/inspect-pre-qa.json` encontrou ganho linear 1,158746, equivalente a aproximadamente 105%, com monitor desabilitado. O auditor verificou a correção da ordem: proteger sink físico e pinos, ajustar Auto-Mute, limitar níveis virtuais e só então habilitar monitor. Ajustes de volume também limitam o nível antes da habilitação; operações de mute revalidam monitor e limitam o nível antes de desmutar, permitindo recuperar nós virtuais recriados. Os mutes do mic virtual e do USB são independentes: silenciar somente a saída processada não protege uma aplicação ligada ao mic cru. Por isso o daemon verifica os dois e informa proteção parcial quando não consegue confirmar ambos.

Na troca AKG/SP166, o mute do sink físico protege a transição enquanto ALSA isola a porta inativa. A memória de preset da sessão é reaplicada após estabilização e reconciliada com eventos do EasyEffects. Esses mecanismos foram revisados e exercitados com fakes; o silêncio efetivo e o comportamento dos guards reais ainda dependem de escuta humana.

A migração de stream cru é manual e exige confirmação por aplicativo. O adaptador aceita um único stream de captura ativo, rejeita ambiguidade e só confirma após observar um link ativo da fonte processada e ausência de link físico ativo. A semântica de `target.object` foi conferida na fonte primária local `/usr/share/wireplumber/scripts/policy-node.lua`, que aceita o nome do nó como destino. Nenhuma migração real foi executada. A identidade do stream é revalidada no readback; comandos CLI sobre IDs efêmeros continuam sujeitos à janela entre leitura e escrita, devendo a validação humana incluir encerramento/recriação de streams.

## Provas executadas

Execução independente completa, incluindo metadata/proxy GJS: `./scripts/quality-gate.sh`, **exit 0**, em `/tmp/easyprofile-auditor-final-quality.log`. A conferência posterior de ordem de segurança, pack e `go mod tidy` usou revisão direta e o gate final do executor em `docs/evidence/quality-gate.log`. Nesse log constam a regressão de inicialização com 105%, a execução GJS privada e o resultado 6/6. O auditor comparou ainda o conteúdo do ZIP final com as fontes, sem recompilar nem instalar.

| Verificação | Resultado |
|---|---|
| `gofmt -s -l cmd internal` | Saída vazia |
| `go vet ./...` e `/home/toru/go/bin/staticcheck ./...` com modo de teste | Sem diagnósticos |
| Compilação da CLI/daemon com `-race` | PASS |
| Sintaxe `node --check`, schema `glib-compile-schemas --strict --dry-run` e metadata ↔ schema | PASS; versão Shell 46 e ligação de `getSettings()` verificadas |
| ZIP final da extensão | Fontes `extension.js`, `dbus.js`, metadata e XML do schema idênticas byte a byte ao conteúdo do arquivo; vínculo metadata/schema confirmado pelo auditor |
| Proxy compartilhado `dbus.js` em GJS/Gio real, barramento privado e daemon fake | PASS: método Remote signed-int, argumento `Gio.Cancellable`, resposta e `StateChanged`; sem carregar a UI no GNOME Shell |
| `EASYPROFILE_TEST_MODE=1 go test -race ./...` | PASS |
| D-Bus privado | Todos os 14 métodos públicos, sinais JSON, tipos, erros e ausência de exportação de lifecycle exercitados |
| CLI ↔ daemon com fakes | Subcomandos de controle exercitados, verificações do estado final e zero comandos de áudio nas sentinelas |
| Invariantes de segurança | Snapshot divergente, override, reset, falha de rota/volume, fila cheia, snapshot ilegível, mute parcial, coalescência, observe-only e ordem segura de inicialização com 105% cobertos |
| Latência IPC em memória, com race | Média de **0,685389 ms** em 100 chamadas GetStatus no log final do executor; execução independente anterior: 0,858114 ms; ambas abaixo de 20 ms |
| `BenchmarkParseGraph` no fixture | **2.823.898 ns/op**, aproximadamente 2,82 ms; 367.174 B/op; 7.601 allocs/op no log final do executor; execução independente anterior: 3,42 ms |

A medição de desempenho em `docs/evidence/performance-fake.json`, produzida pelo executor e inspecionada pelo auditor, registra janela de 60,0003 s, RSS de 8.168 → 8.040 KiB e CPU de 0,0 s. Refere-se a binário sem race, barramento privado e adaptadores fake; não mede watchers reais, subprocessos de áudio ou GNOME Shell. O auditor não repetiu essa janela de 60 s.

## Integridade do setup atual

As consultas read-only confirmaram `ka1333-input-guard.service` e `easyeffects-output-guard.service` **active / active**. O gate final voltou a verificar ambos.

Os três arquivos correspondem integralmente à baseline SHA-256:

| Preset | SHA-256 confirmado |
|---|---|
| `input/Preset KA-1333.json` | `2b31d5fc18d5710026de5e57469a7021f5f67e1aa6f9b4f4e586033f8195e170` |
| `output/AKG K52 - Uso diario aprovado.json` | `c7af0ffd83f1c7598f477c1789825a321e2a1570309fd5f426b32d39cd57d669` |
| `output/SP166 - Uso diario aprovado.json` | `7802bfbf95d5fe70d1964e6cfca63adc90a86ed26d78837dd45b2f2a3a142f52` |

A baseline contém caminhos com `~`; `sha256sum -c` diretamente não expande esse caractere. A prova usou expansão explícita do diretório pessoal e comparação dos hashes, também feita pelo gate. Essa diferença do comando literal da matriz não representa alteração dos presets.

## Conformidade e pendências de aceite

O código final examinado atende aos contratos locais de IPC único, domínio, execução segura, fakes, proteção de teste e preservação do setup estabelecidos no `RULES.md`. Os mecanismos previstos para EP-004/EP-005 no DRS estão implementados e têm as provas locais listadas. Não é possível declarar conformidade operacional integral sem a execução acompanhada dos adaptadores e da extensão no desktop real.

O teste GJS executa o mesmo módulo de proxy importado pela extensão, sem simular as classes Gio. Isso comprova esse componente de runtime e seu contrato D-Bus. O empacotamento também foi corrigido para associar o schema e expandir os sliders. Permanecem fora da prova automatizada: captura efetiva das teclas no GNOME 46/Wayland, ausência de vazamento de caracteres, OSD/sliders/menu, reconexão real da extensão, atenuação audível, silêncio da porta inativa, Dual-Mute físico, autoload sob os guards e migração efetiva de uma aplicação real. A extensão não foi instalada nem ativada durante esta auditoria. O usuário adiou o QA humano; não houve acionamento real de volume/mute/rota para concluir esta entrega.

Identidade da sessão gráfica, política de inicialização por login, instalação persistente, limites systemd, cutover e rollback dos guards continuam pertencendo à EP-006. Nenhum resultado deste parecer autoriza essas ações.

## Roteiro de validação humana

1. Registrar mutes, rota, volumes e presets atuais; manter ambos os guards ativos e prever restauração imediata do snapshot. Começar pelo daemon em observação e conferir o status com o desktop.
2. Em sessão acompanhada, validar NumLock OFF/ON, todas as teclas mapeadas, repetição de ±2%, No-Op de 4/6/7/8/9 e ausência de caracteres no terminal/editor.
3. Conferir ícone, badge, modo de áudio, sliders, mutes, presets filtrados, reset e reconexão após reinício do daemon e disable/enable da extensão.
4. Com áudio em nível seguro, validar AKG ↔ SP166 e silêncio da porta inativa; conferir volumes por rota e memória de preset vencendo o autoload.
5. Validar Dual-Mute e pânico com mutes inicialmente divergentes; executar override manual e reset, verificando os dois alvos do mic separadamente.
6. Com uma aplicação de captura descartável, verificar alerta de mic cru e migração explicitamente confirmada; conferir novo link DSP e remoção do acesso físico ativo. Exercitar desaparecimento/recriação do stream sem aceitar sucesso indevido.
7. Restaurar o snapshot inicial e repetir a consulta dos guards e a comparação dos três hashes.

A liberação final depende dessa validação humana, revisão deste parecer pelo usuário e autorização explícita de commit, conforme a instrução da fatia.
