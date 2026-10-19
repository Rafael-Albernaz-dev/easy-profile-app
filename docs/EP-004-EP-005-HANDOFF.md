# EP-004 + EP-005 — Relatório de engenharia e handoff

Data: 18/10/2026. Repositório: `main`, base `647f5bc`, alterações locais sem commit.

**Estado de entrega: implementação e gates automatizados aprovados; QA humano pendente.** O usuário escolheu deixar o ensaio de desktop para depois. O daemon real de controle não foi iniciado e a extensão não foi instalada/ativada nesta execução.

## 1. O que foi feito na engenharia/código

- Adaptadores PipeWire, ALSA e EasyEffects reais, com subprocessos sem shell, prazos, argumentos explícitos e bloqueio quando `EASYPROFILE_TEST_MODE` está presente. Fakes em memória são usados nos testes.
- PipeWire resolve IDs atuais e o prefixo do KA-1333, escreve `monitor.channel-volumes` em SPA `Props.params`, converte ganho linear para a escala cúbica do wpctl e confirma readbacks. ALSA confirma Auto-Mute e controles dos pinos. EasyEffects lê os JSONs sem alterá-los, rejeita colisão input/output e confirma preset, cadeia ordenada e bypass.
- Daemon Session D-Bus em `org.easyprofile.Daemon`, `/org/easyprofile/Daemon`, com métodos explicitamente exportados, introspecção, estado JSON atômico e `StateChanged`. Estado desejado e observado são separados; falhas de privacidade não são reportadas como sucesso.
- Fila serial com coalescência de níveis adjacentes, expiração do trabalho normal e prioridade de pânico que aceita fila normal cheia. O pânico cancela trabalho normal em andamento; uma troca interrompida conserva o mute físico. Leituras do snapshot e tentativas de mute têm prazos independentes. Snapshot incompleto permite mutar, mas impede restauração automática insegura.
- Snapshot de três booleans preservado até os adaptadores, inclusive divergência do mic, override durante pânico e reset. Restauração de sessão com mic em 0% e validação de presets corrigidas; as entidades retornadas pelo controller são cópias.
- CLI com `daemon`, `status [--json]`, `route`, `volume`, `mic`, `mute`, `panic`, `preset`, `reset` e o comando de inspeção preservado. Conexões próprias e chamadas com timeout. `daemon` usa observação por padrão; `--control` habilita alterações reais; `--fake` exige modo de teste.
- `config.example.json` e `daemon --config <arquivo>` desacoplam nomes de hardware, rotas, pinos, tetos, presets e janela de estabilização. O teto AKG acima de 97% é rejeitado.
- Extensão GNOME 46 com indicador, badge, modo NumLock, menu, sliders, presets filtrados, mutes, reset, alerta de mic cru e confirmação de migração por aplicativo. O módulo `dbus.js` é compartilhado com a prova GJS. Callbacks são invalidados ao desativar a extensão ou mudar o proprietário do serviço; OSD segue confirmação do daemon.
- Migração manual via metadata WirePlumber, sem automigração: exige captura ativa única e confirma DSP ativo sem link físico ativo. Aplicações com múltiplos streams de mesmo nome são recusadas.
- Empacotamento inclui explicitamente `dbus.js`; metadata aponta o schema correto e o gate confere o conteúdo do ZIP.

A política definitiva de login/sessão gráfica, persistência entre reinícios, unidade systemd, instalação persistente do daemon, limites e cutover/rollback continuam na EP-006. O daemon desta fatia mantém memória durante sua própria execução e adota mutes observados ao iniciar.

## 2. O que isso significa no áudio

O número apresentado pelo GNOME/wpctl usa escala cúbica: 50% corresponde a ganho linear 0,125. A porta do adaptador usa ganho linear e faz uma única conversão para o wpctl; os passos ficam em porcentagens inteiras para evitar drift.

O controle de saída atua no nó virtual do EasyEffects. Para que a atenuação seja efetiva nesse caminho, `monitor.channel-volumes` precisa estar ligado. No microfone, o nível ajusta a saída processada pelos filtros, preservando o ganho USB físico calibrado. O mute de privacidade atua separadamente na fonte processada e no USB; cada alvo precisa confirmar seu próprio estado.

Trocar AKG/SP166 é trocar a porta da mesma placa. O daemon protege primeiro o sink físico e os dois pinos ALSA; só então configura Auto-Mute, níveis virtuais limitados, monitor gain, rota, teto físico e preset. A porta inativa é reafirmada e a proteção só é liberada depois das confirmações. O preset escolhido na sessão é reaplicado após a janela de estabilização e reconciliado com eventos de autoload.

A inspeção passiva em `evidence/inspect-pre-qa.json` encontrou AKG frontal ativo, teto físico 97%, `Front` em 0%/mute, Auto-Mute Disabled e pedido virtual de aproximadamente **105% com monitor gain desligado**. Esse pedido preexistente não comprova amplificação audível. A inicialização foi corrigida e tem regressão para limitar o pedido antes de habilitar o ganho, com o hardware já silenciado. Nenhum desses valores do host foi alterado nesta execução.

A implementação, os readbacks programados e os fakes não comprovam a percepção sonora, o silêncio de um conector físico nem o comportamento de uma chamada real. Essas verificações permanecem no QA humano.

## 3. Integridade do setup atual

Os dois serviços foram consultados por `systemctl --user is-active` e permaneceram **active / active**:

- `ka1333-input-guard.service`
- `easyeffects-output-guard.service`

Os três presets tiveram SHA-256 idêntico à baseline antes/depois das verificações e no gate final. Nenhum serviço foi parado/reiniciado e nenhum preset foi escrito. O manifesto usa `~`; o gate expande esse caminho explicitamente, pois `sha256sum -c` não o expande sozinho.

Governança privada segue fora dos arquivos rastreados. Não houve stage, commit, push ou cutover.

## 4. Provas executadas e roteiro humano

O [parecer independente](audit-ep004-ep005.md) registra o aceite técnico local condicionado à homologação humana. O [log final dos seis gates](evidence/quality-gate.log) registra formatação, vet/staticcheck, build, higiene, integridade, schemas/metadata/ZIP, testes `-race`, D-Bus privado, proxy GJS real e benchmark.

A integração usa fakes por construção, barramento privado obrigatório, diretórios temporários, GSettings em memória e PATH com sentinelas para ferramentas de áudio, incluindo `pw-metadata`; terminou com zero comandos nessas sentinelas. Os 14 métodos D-Bus, sinais e subcomandos foram exercitados. O teste GJS executa o proxy real Gio com argumentos signed-int e cancelamento, sem carregar a UI no Shell.

A última rodada do executor mediu GetStatus em memória em cerca de **0,69 ms** e parse do fixture em **2,82 ms**. O [ensaio isolado de 60 segundos](evidence/performance-fake.json) registrou RSS de 8.168 → 8.040 KiB e nenhum incremento de ticks de CPU. Esse ensaio usa fakes e binário sem race; não mede custo dos adaptadores reais, seus observadores ou da extensão.

### Artefatos preparados

- `bin/easyprofilectl`: binário gerado localmente; `make build` produz a versão comum sem race.
- `dist/easyprofile@gnome-shell.shell-extension.zip`: pacote local da extensão, conferido contra as fontes; gerar com `make extension-pack`.
- `config.example.json`: configuração calibrada do setup.

### Retomada acompanhada do QA

1. Atualizar a inspeção passiva e anotar o estado inicial de rota, volumes, mutes e presets. Conferir ambos os guards. Não usar o snapshot antigo como se fosse o estado atual de uma sessão futura.

   ```bash
   make build extension-pack
   bin/easyprofilectl inspect --json > /tmp/easyprofile-pre-qa.json
   systemctl --user is-active ka1333-input-guard.service easyeffects-output-guard.service
   bin/easyprofilectl daemon --config config.example.json
   ```

   Esse daemon inicial permanece em observação. Usar outro terminal para `bin/easyprofilectl status --json`; Ctrl+C encerra somente esse processo.

2. Na sessão acompanhada, instalar o pacote e conferir a extensão. Se o GNOME Wayland não reconhecer uma extensão nova na sessão atual, retomar após logout/login manual; não reiniciar o Shell à força.

   ```bash
   gnome-extensions install dist/easyprofile@gnome-shell.shell-extension.zip
   glib-compile-schemas ~/.local/share/gnome-shell/extensions/easyprofile@gnome-shell/schemas
   gnome-extensions enable easyprofile@gnome-shell
   gnome-extensions info easyprofile@gnome-shell
   ```

3. Encerrar o daemon em observação e, com o usuário pronto para ouvir e sem depender de uma chamada em andamento, iniciar em primeiro plano:

   ```bash
   bin/easyprofilectl daemon --control --config config.example.json
   ```

   O modo de teste deve estar ausente nessa sessão real. Não desativar os guards. O início limita os níveis e protege a rota; não reproduz o desmute de login definitivo da EP-006.

4. **Numpad:** confirmar NumLock OFF liberando uso normal e ON capturando as teclas. Testar `+/-` saída, `*//` mic, `0` mic, `.`/`,` saída, Enter pânico, `1` AKG, `2` SP166, `3` alternância e `5` reset. Segurar teclas para distinguir repetição de níveis dos toggles. Confirmar 4/6/7/8/9 silenciosos e sem caracteres em terminal/editor. NumLock muda o modo; as outras 16 teclas são as ações/bloqueios registrados.
5. **Top Bar:** conferir ícone/badge/modo, sliders, mutes, presets filtrados, reset e OSD. Reiniciar somente o daemon deste ensaio e testar reconexão; testar disable/enable da extensão, inclusive enquanto há ajuste pendente.
6. **Áudio:** começar baixo, comprovar atenuação audível e alternância AKG/SP166 com silêncio no conector inativo. Conferir memória de volume/preset por rota e autoload sob os guards. Verificar que o ganho físico USB permanece calibrado.
7. **Privacidade:** observar separadamente mutes virtual/físico; validar pânico com estados inicialmente divergentes, override manual e reset. O reset volta a 95%/100% e preserva os dois mutes atuais do mic; não é um restore completo de snapshot.
8. **Mic cru:** usar uma aplicação de captura descartável, verificar alerta e confirmar sua migração no submenu. Conferir link processado e ausência de acesso físico ativo. Testar desaparecimento/recriação do stream e recusa de ambiguidade.
9. Encerrar o ensaio, devolver rota/volumes/mutes/presets ao estado inicial registrado com acompanhamento e readback. Desativar a extensão se não for continuar usando e encerrar somente o daemon do ensaio. Conferir novamente ambos os guards e os três hashes. Não aplicar rollback/cutover de serviços nesta fatia.

   ```bash
   gnome-extensions disable easyprofile@gnome-shell
   systemctl --user is-active ka1333-input-guard.service easyeffects-output-guard.service
   ```

O QA humano foi adiado por escolha do usuário. A liberação de uso real e qualquer commit continuam dependendo dessa validação, da revisão dos relatórios e de autorização explícita.
