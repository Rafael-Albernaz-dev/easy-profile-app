# EasyProfileApp — Requisitos e Arquitetura v2 — Remediação

Data: 19/10/2026. Contrato consolidado a partir da missão de remediação aprovada pelo usuário. Provas locais com fakes não representam homologação acústica, instalação da nova extensão ou autorização de cutover.

## 1. Escopo e decisões

Ferramenta pessoal para Ubuntu 24.04, GNOME Shell 46 Wayland, PipeWire 1.0.5, WirePlumber 0.4.17 e EasyEffects 7.1.6. AKG K52 frontal, SP166 traseiro e mic USB KA-1333. Configuração de identidades, rotas, presets compatíveis e tetos em `config.example.json`.

- Go único para CLI/daemon e GJS ESM para GNOME. Session D-Bus é o único IPC: `org.easyprofile.Daemon`, objeto `/org/easyprofile/Daemon`.
- Sem WebSocket/HTTP ou interfaces ornamentais. Portas PipeWire/ALSA/EasyEffects expressam operações usadas pelo daemon e pelos fakes.
- `AudioController` encapsula estado desejado, memória por porta, compatibilidade de presets e invariantes de privacidade. Persistência de sessão é pequena, síncrona e atômica; erros aparecem no status e no resultado do comando.
- Daemon tem um worker serial, prioridade/cancelamento para pânico e agrupamento apenas de volumes adjacentes do mesmo canal/destino. Status é uma string JSON imutável publicada atomicamente, sem adquirir o lock de hardware.

## 2. Contrato acústico

**Volumes:** saída virtual e nível pós-DSP do mic vão de 0 a 100%, em passos inteiros de 1%. O domínio usa ganho linear na porta; `wpctl` recebe escala cúbica. O ganho físico USB do mic permanece intocado. O teto físico é ≤97% no AKG e ≤100% no SP166.

**Troca de rota:** silenciar sink físico e os dois pinos ALSA; garantir Auto-Mute Disabled; confirmar vínculos DSP; limitar volumes antes de habilitar monitor gain; trocar e confirmar rota; manter pino inativo mutado; estabilizar autoload; aplicar preset de sessão; confirmar níveis/mutes/cadeia; só então liberar o sink físico. Falhas mantêm mute físico e são reportadas.

**Dual-Mute e pânico:** microfone virtual e físico são alvos independentes. O snapshot tem `{SavedOutMute, SavedMicVirtualMute, SavedMicPhysicalMute}`. Overrides manuais atualizam seus campos. Snapshot incompleto jamais autoriza restauração automática. Reset `*` restaura preset diário, saída 95%, mic 100% e preserva os dois mutes atuais do microfone. Reset é também ação no menu/CLI, sem reinício implícito.

**Sessão:** memória de volume/preset é por destino e por identidade de login. Reinício no mesmo login restaura os níveis e preset; login novo recebe base diária. O marcador `login_session.json` é separado de `session.json`, ambos privados e escritos por CreateTemp/Sync/Rename/Sync do diretório. A permissão de login é consumida duravelmente antes de qualquer desmute automático. Se uma etapa posterior falhar, reiniciar adota os mutes observados e não repete o desmute. Marcador inválido/ilegível, identidade desconhecida ou snapshot ausente no mesmo login falham seguros.

**Exclusividade:** flock não bloqueante em `$XDG_RUNTIME_DIR/easyprofile.lock` (fallback `/run/user/<uid>/easyprofile.lock`) antes de carregar sessão e antes de qualquer ação no áudio. O arquivo não é removido ao liberar o lock. A identidade gráfica exige `XDG_SESSION_ID` ou `SESSION_MANAGER`; nomes de sockets reutilizáveis não substituem essa identidade.

**Guardas DSP substitutas:** na subida e reconciliação, manter sink virtual como default e configured default; saída do EasyEffects na placa física; entrada do EasyEffects no KA-1333 identificado por USB/perfil; seleção explícita de dispositivos; `monitor.channel-volumes=true` nos dois nós virtuais. Auditoria de grafo e readbacks alimentam `dsp_bound` e `monitor_volumes_confirmed`. Uma reparação necessária usa a sequência protegida, sem autorizar desmute novo do mic em background. Rota externa inesperada recebe mute físico e exige seleção explícita.

**Streams crus:** identificar links ativos de captura física que bypassam DSP, alertar e permitir somente migração manual por aplicação. Recusar ambiguidade e confirmar links processados e desaparecimento dos crus.

## 3. Mapa oficial das 17 teclas

| Tecla | Ação | Repetição |
|---|---|---|
| `NumLock` | Liga/desliga modo áudio (`⚡`); desligado libera o teclado | Nativa |
| `+` | Volume +1% no canal focado, limitado a 100% | Sim |
| `-` | Volume −1% no canal focado, limitado a 0% | Sim |
| `1` | Seleciona AKG K52 e foca saída | Não |
| `2` | Seleciona SP166 e foca saída | Não |
| `3` | Alterna foco de volume entre saída e microfone | Não |
| `4` | Preset compatível 1 do destino ativo | Não |
| `5` | Preset compatível 2 do destino ativo | Não |
| `6` | Preset compatível 3 do destino ativo | Não |
| `0` | Dual-Mute do microfone; indicador vermelho no painel | Não |
| `.` / `,` | Mute da saída (`KP_Decimal` / `KP_Separator`) | Não |
| `Enter` | Pânico: saída e microfone; restauração por snapshot independente | Não |
| `/` | DynamicEqualizer placeholder; somente registra intenção | Não |
| `*` | Reset diário: preset base, saída 95%, mic 100%, preservando ambos os mutes do mic | Não |
| `7` | Pausada, no-op silencioso | Não |
| `8` | Pausada, no-op silencioso | Não |
| `9` | Pausada, no-op silencioso | Não |

NumLock controla o registro dos outros 16 atalhos. Somente `+` e `-` usam `Meta.KeyBindingFlags.NONE`; demais ações usam `IGNORE_AUTOREPEAT`. `4/5/6` selecionam presets; não são no-op. Não existe Reset em `5`. A vírgula e o ponto representam o mesmo controle decimal em variantes do teclado.

## 4. Contrato da extensão e D-Bus

ESM nativo/`gi://`, chamadas assíncronas com Gio.Cancellable, proteção por geração e proprietário do nome D-Bus. `disable()` cancela chamadas, remove timers rastreados, desconecta sinais, remove bindings e destrói/libera atores.

Fila de níveis usa `${channel}-${destId}`. Seleção de destino, preset e Reset invalidam pendências; barreiras aguardam chamadas já enviadas e a confirmação da nova operação antes de aceitar novos níveis. `SetVolumeForDestination(destination, percent)` também valida o destino dentro do worker, impedindo aplicação atrasada na outra saída. Sinais atrasados não apagam um alvo de volume ainda pendente. Feedback imediato é do label de nível; ícones usam o estado publicado e a declaração de privacidade exige readback dos dois alvos. OSD é reservado a erros.

Métodos públicos: GetStatus; SetVolume/SetVolumeAbsolute/SetVolumeForDestination; SetMicVolume/SetMicVolumeAbsolute; ToggleOutputMute/ToggleMicMute/TogglePanic; SelectDestination/ToggleDestination/SelectPreset; ResetDefaults; GetRawMicStreams/MigrateRawMicStream. Sinal StateChanged carrega JSON. Não há Restart, configuração ou ciclo de vida exportado. Reinício explícito pelo menu usa somente systemd.

Métodos e introspecção são exportados antes de RequestName; o flock já assegura exclusividade durante a inicialização anterior ao anúncio.

## 5. Verificação e performance

Todos os testes usam `EASYPROFILE_TEST_MODE=1` e fakes de áudio. Adaptadores reais bloqueiam execução quando essa variável está presente. Integração usa `dbus-run-session` com configuração sem diretórios de ativação, runtime temporário, GSettings em memória e sentinelas de ferramentas de áudio.

1. Formatação, vet, staticcheck e compilação com race.
2. Auditoria Go AST de subprocessos/arquivos, testes sem os/exec e higiene privada no Git/ambas as listas de exclusão. Não é uma prova formal contra toda forma possível de escrita.
3. Guards ativos, hashes dos 3 presets, sintaxe/metadata/schema e conteúdo do pacote GJS.
4. Go com race, regressões de cutover por executáveis falsos e métodos reais da extensão contra atores em memória.
5. CLI ↔ daemon ↔ proxy GJS real em barramento privado, inclusive cancelamento de chamada pendente e exclusividade de processo. Não carrega a UI no Shell.
6. Parse de dump capturado <5 ms; status direto durante bloqueio de hardware <5 ms; CLI fake p95 <15 ms (100 amostras, máximo reportado); daemon fake RSS <15 MB; CPU fake ociosa <1% de um núcleo por 60 s. Recursos reais com watchers/subprocessos/GNOME continuam sujeitos a medição acompanhada.

## 6. Cutover e rollback

Os 2 guards de produção permanecem ativos e os 3 presets imutáveis no desenvolvimento. Esta remediação implementa scripts e testes, sem executar o cutover.

Dry-run é somente leitura, sem build, criação de diretórios ou backup. O snapshot dos guards é JSON validado, criado atomicamente sem substituir um anterior. Estados transitórios/desconhecidos são recusados antes de mutações. Estados suportados: active/inactive e enabled/enabled-runtime/disabled/masked/masked-runtime/static/indirect.

A partir da primeira desativação de guard, ERR/INT/TERM conduzem ao rollback. Saúde exige `protection_state=confirmed`, `last_error=""` e modo de controle, com tentativas limitadas por prazo. SIGKILL ou perda de energia não executam traps; o snapshot preservado permite recuperação explícita.

Rollback remove ativação D-Bus antes de parar/desabilitar o daemon, tenta restaurar ambos os guards mesmo diante de uma falha, verifica exatamente estados positivos/negativos e hashes de presets. Backup ausente/corrompido não autoriza fallback que desmuta/ativa indiscriminadamente.

## 7. Estado de homologação e backlog

EP-006 possui implementação local e regressões herméticas; homologação de login no systemd real, consumo com adaptadores reais, escuta, GNOME Wayland e cutover/rollback reais permanecem pendentes de sessão acompanhada e autorização própria. Relatórios antigos são históricos e não certificam o checkout atual.

DynamicEqualizer permanece placeholder em `/`. DSP dinâmico in-stream e análise de contexto são backlog, sem arquivo de pesquisa ou prova de latência adicional presumidos. Não alterar a cadeia calibrada ou presets originais como parte desta remediação.
