# Handoff atual — Remediação de segurança EasyProfileApp

Data: 19/10/2026. Este documento substitui as instruções operacionais antigas de EP-004/EP-005 para o checkout atual. O parecer anterior em `audit-ep004-ep005.md` e evidências antigas permanecem históricos. A nota de maturidade antiga não representa aceite do novo código.

## 1. Engenharia implementada

**Slice 1:** instalador com dry-run sem escrita/build, leitura resiliente de systemd, snapshot atômico sem sobrescrita e trap de rollback após início do cutover. Saúde por JSON confirmado, sem LastError e em controle. Rollback remove ativação D-Bus, interrompe o novo daemon e restaura simetricamente active/enabled de cada guard, verificando hashes. Testes usam HOME em `/tmp` e executáveis simulados, incluindo falha de partida/saúde, estados negativos/runtime, preservação do backup e alteração de hash em fixture falsa.

**Slice 2:** marcador de login falha seguro e usa gravação atômica durável. Flock precede leitura de sessão e hardware. Snapshot de volumes/presets é separado do marcador, vinculado ao login e validado; reinício recupera ambas as portas. Falhas de I/O são reportadas. Grafo DSP/defaults/dispositivos e monitor gain são auditados/reparados; cache reage a eventos, identidade e erros. Status publicado não disputa o lock do worker.

**Slice 3:** volumes vinculados ao destino, comando D-Bus com validação de destino e barreiras de rota/preset/Reset. Callbacks atrasados não ressuscitam pendências nem atores; timers são removidos. Restart D-Bus foi removido; menu reinicia somente via systemd e `*` apenas reseta. Contrato das teclas consolidado abaixo. Gates incluem AST, regressões de cutover/UI, bus privado sem ativação e performance explicitamente fake.

## 2. O que isso significa no áudio

Os guards atuais continuam sendo a proteção de produção. O código novo prepara sua futura substituição: manter apps no sink virtual, EasyEffects na placa e no KA-1333 correto, ganho de monitor habilitado e rota protegida por mute físico até os readbacks terminarem.

Um número de volume na UI não comprova ganho audível. Um readback de seleção do dispositivo não comprova silêncio no P2, ausência de estalos ou qualidade da cadeia DSP. Os fakes provam sequenciamento, estados e falhas; a confirmação física permanece no roteiro acompanhado.

No reinício do mesmo login, níveis pequenos não voltam automaticamente a 95%/100%. Estado inválido não vira permissão de desmute. Na recuperação do grafo em background, os mutes do microfone são preservados ou reforçados. O pânico conserva estados independentes; sair dele não colapsa mute físico/virtual. Reset preserva os dois mutes do mic, mas restaura saída 95% e mic 100%, conforme contrato.

## 3. Contrato das 17 teclas

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

NumLock controla as outras 16 teclas. Só `+/-` repetem. `*` não reinicia o processo. O DynamicEqualizer ainda não altera DSP.

## 4. Integridade e provas locais

Durante desenvolvimento, consultar apenas `systemctl --user is-active` para os dois guards e ler/hash dos três presets. Não executar os scripts reais de instalação/rollback, restart, comandos avulsos de áudio nem instalar a nova extensão para substituir essa prova hermética.

```bash
EASYPROFILE_TEST_MODE=1 GSETTINGS_BACKEND=memory go test -race ./...
make quality-gate
```

O gate mantém `EASYPROFILE_TEST_MODE=1`; a integração cria um bus privado sem ativação e usa runtime/config temporários. O teste de proxy GJS usa Gio real, inclusive cancelamento de chamada pendente, mas não executa a extensão no GNOME Shell. Cancelar a chamada do cliente não desfaz uma operação já aceita pelo daemon; barreiras de UI e validação do destino tratam essa concorrência.

O Gate 6 mede um binário comum com fakes por 60 s e 100 chamadas CLI; não mede CPU/RAM de watchers e subprocessos reais. Registrar separadamente parse, status sob bloqueio, CLI p50/p95/máximo, RSS e CPU. A meta projetada 95+/100 para a remediação de engenharia não substitui nova revisão independente ou aceite físico.

## 5. Observação sem controle

**É obrigatório usar `--observe`**. O daemon padrão tem controle habilitado. O comando sem essa flag nunca deve ser apresentado como observação.

```bash
make build extension-pack
# Inspeção passiva não inicia daemon:
bin/easyprofilectl inspect --json
# Somente se não houver outro daemon com o lock da sessão:
bin/easyprofilectl daemon --observe --config configs/config.example.json
```

Não lançar um segundo daemon para disputar um processo humano. Em modo observe o código não repara hardware nem consome a política de login. Para produção, confirmar disponibilidade de `XDG_SESSION_ID` ou `SESSION_MANAGER` no ambiente do serviço; sockets estáveis de Wayland/D-Bus não autorizam desmute.

## 6. Roteiro humano futuro — não executado nesta remediação

1. Com autorização própria, registrar rota, volumes, cada mute virtual/físico, preset e hashes. Confirmar guards e identificar processos responsáveis antes de qualquer mudança.
2. Validar a extensão em GNOME Shell 46 Wayland: NumLock OFF/ON, 17 teclas, repetição apenas de +/- e sinalização de mic/erros; disable/enable com chamadas e retries pendentes.
3. Começar com volume baixo. Testar troca AKG/SP166, silêncio físico do conector inativo, tetos calibrados, ausência de estalos e memória de volume/preset por porta. Testar ajuste rápido seguido de rota/preset/Reset.
4. Validar dual-mute e pânico com estados divergentes, overrides e Reset. Confirmar que ganho físico USB não muda. Comparar reinício no mesmo login com novo login gráfico e falhas de estado/armazenamento em ambiente descartável.
5. Validar hotplug do KA-1333, reinício do EasyEffects, defaults e vínculos reais, monitor gain e autoload. Confirmar alerta de captura crua e migração explícita por links.
6. Medir CPU/RAM/latência reais, incluindo watchers e filhos, sem inferir esses valores dos fakes. Definir limites systemd com margem.
7. Somente depois do aceite anterior e autorização específica, testar cutover/rollback reais. Preservar snapshot dos guards; um backup anterior não deve ser removido automaticamente para permitir tentativa nova. SIGKILL/perda de energia exigem recuperação explícita, não uma promessa de trap.
8. Restaurar o estado inicial acompanhado e conferir guards/hashes. Commit, push, instalação e cutover não foram executados como parte da remediação local.
