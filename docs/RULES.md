# Architecture Rules, Guardrails & Quality Gates (`EasyProfileApp` v2)

Este documento consolida as regras de engenharia, os guardrails de proteção do áudio em produção, o isolamento hermético de testes e os portões de qualidade proporcionais do `EasyProfileApp`, revisados após a auditoria técnica cruzada.

---

## 1. Guardrails de Segurança do Áudio em Produção

1. **Preservação dos Guards Atuais até a Slice `EP-006`:**
   - Os serviços `ka1333-input-guard.service` e `easyeffects-output-guard.service` permanecem ativos e habilitados durante as slices `EP-001` a `EP-005`.
   - No cutover (`EP-006`), o instalador salva o estado anterior (`is-active` / `is-enabled`) de cada serviço para que o rollback restaure exatamente a condição prévia (sem `enable --now` cego).
2. **Exceção Controlada para a Slice `EP-002` (Spikes Acompanhados):**
   - Todo daemon de desenvolvimento sem controle exige `--observe` (ou alias `--observe-only`) explícito. O default da CLI é controle e não deve ser usado como observação.
   - Na Slice `EP-002`, são permitidos testes pontuais e acompanhados pelo usuário para validar volume/mute/rota reais, desde que: (a) não desabilitem os guards ativos, (b) façam snapshot do estado anterior antes do comando, e (c) restaurem o estado imediatamente após a medição.
3. **Imutabilidade dos Presets Originais do EasyEffects:**
   - Os arquivos em `~/.config/easyeffects/output/` e `~/.config/easyeffects/input/` jamais são modificados pelo `EasyProfileApp`.
4. **Execução Segura de Subprocessos:**
   - Proibido usar `bash -c` ou `sh -c`. Todo comando externo nos adaptadores reais usa `exec.CommandContext` com argumentos explícitos e timeout.

---

## 2. Isolamento Hermético de Testes Automatizados (Anti-Gaming)

> [!IMPORTANT]
> **Lição da Auditoria:** `dbus-run-session` isola apenas o barramento D-Bus; ele **NÃO** isola o socket do PipeWire, o hardware ALSA nem o banco `dconf` do usuário!

Portanto, para garantir que `go test ./...` e `./scripts/quality-gate.sh` tenham **Zero Efeito Colateral no Host**:
1. **Fakes por Construção:** Nos testes automatizados, o daemon e o controlador recebem exclusivamente adaptadores em memória (`FakePipeWire`, `FakeAlsa`, `FakeEasyEffects`) via injeção de dependência.
2. **Bloqueio por Variável de Ambiente e `PATH` em Testes:** A suíte de testes define `EASYPROFILE_TEST_MODE=1` (que faz os adaptadores reais de comando recusarem execução com panic/erro caso sejam instanciados por engano em um teste) e utiliza diretórios temporários (`t.TempDir()`) + `GSETTINGS_BACKEND=memory` quando aplicável.
3. **Zero Testes Cosméticos:** Só escrevemos testes unitários e de regressão para invariantes reais de áudio/estado (ex: Snapshot de 3 booleans do Pânico, override manual durante o Pânico, arbitragem de preset da sessão vs autoload, sufixo dinâmico do KA-1333, fragmentos truncados do `pw-dump`).

---

## 3. Metas de Performance e Recursos (Medir Antes de Limitar)

Em vez de impor um `CPUQuota=2%` no `systemd` que pode causar *throttling* de CPU justamente durante o spawn de subprocessos de troca de rota ou mute:
1. **Medição Reproduzível:** Medimos nas slices `EP-004` e `EP-006` a latência real de IPC D-Bus, o tempo de parse do dump do PipeWire e o RSS real do processo (`ps`/`/proc/<pid>/status`) + subprocessos.
2. **Metas Operacionais:**
   - Consumo de CPU em repouso (sem eventos no grafo): desprezível (`~0%` numa janela de 60s).
   - Leitura direta de status durante bloqueio de hardware: `< 5 ms`, sem disputar o lock do worker.
   - CLI status com processo + D-Bus privado + daemon fake: p95 `< 15 ms` em 100 amostras; máximo reportado separadamente.
   - Parse de dump capturado: `< 5 ms/op`; daemon fake: RSS `< 15 MB` e CPU ociosa `< 1%` de um núcleo em 60s.
   - Provas fake não certificam recursos/latência de adaptadores reais, subprocessos ou GNOME; estes exigem medição acompanhada antes do cutover.
   - Limites de proteção no `systemd` (`MemoryMax`, `TasksMax`) serão calibrados na `EP-006` com margem segura (ex: `MemoryMax=64M`, sem estrangular o pico de troca de rota com `CPUQuota=2%`).

---

## 4. Quality Gates Proporcionais por Slice (`./scripts/quality-gate.sh`)

O script `./scripts/quality-gate.sh` executa verificações proporcionais aos componentes já existentes na slice atual (sem falhar cobrando arquivos de slices futuras e sem usar falsos positivos como `node --check` para provar semântica do GNOME Shell):

- **Gate 1 (Formatação & Estática):** `gofmt -s -l cmd internal scripts`, `go vet`, `staticcheck` e compilação com race.
- **Gate 2 (Segurança & Higiene Git):** Auditoria Go AST rejeita shells/comandos destrutivos ou sem contexto, executáveis dinâmicos fora do runner de allowlist e testes Go com os/exec; adaptadores/inspect não podem gravar arquivos. Garante governança privada fora de `git ls-files` e presente nas duas listas de exclusão. Essas regras são específicas e não constituem prova formal de toda execução/escrita possível.
- **Gate 3 (Integridade dos Presets & Extensão GJS):** Verifica que os hashes SHA-256 dos 3 presets reais em `~/.config/easyeffects/` permanecem idênticos aos originais e, a partir da slice GJS, valida o schema GSettings (`glib-compile-schemas --strict --dry-run`) e os arquivos da extensão.
- **Gate 4 (Unitários & Regressão):** Go com race e `EASYPROFILE_TEST_MODE=1`, cutover com ferramentas falsas em `/tmp` e métodos da extensão contra atores/proxy em memória.
- **Gate 5 (Integração D-Bus Isolada):** `dbus-run-session` sem diretórios de ativação, runtime temporário e fakes, exercitando CLI ↔ daemon ↔ proxy GJS, cancelamento pendente e exclusividade. Não executa UI no Shell.
- **Gate 6 (Benchmarks):** Impõe os orçamentos capturados/fake acima e verifica novamente guards ativos e hashes intactos no host durante o desenvolvimento.

## 5. Contratos atualizados pela remediação de 19/10/2026

O contrato vigente das 17 teclas está em `docs/ARCHITECTURE_AND_REQUIREMENTS.md`: Reset em `*`, presets em `4/5/6`, foco de mic em `3` e passos de 1%. Reinício somente via systemd, sem método D-Bus de ciclo de vida. Observação explícita não consome a política de login nem repara áudio.

Marcador de login e snapshot de áudio são arquivos distintos, atômicos e privados. Estado inválido/ilegível não autoriza desmute; no mesmo login, snapshot ausente também não autoriza reaplicar ganhos padrão. O flock precede qualquer inicialização de hardware. Cutover e homologação real permanecem pendentes até autorização própria; traps não prometem recuperação automática de SIGKILL/perda de energia.
