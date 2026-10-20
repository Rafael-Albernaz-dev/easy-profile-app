# Compartilhar toda a saída em uma chamada

O menu do EasyProfile oferece **Compartilhar toda a saída na chamada**.
Com uma chamada já capturando o microfone, ligar o botão substitui temporariamente
essa entrada pelo áudio da saída física selecionada. Desligar encerra o transporte
e restaura a política de entrada anterior. O áudio continua chegando ao fone/caixas.
Não há mistura com a voz do microfone.

CLI equivalente: `easyprofilectl share on`, `easyprofilectl share off` ou
`easyprofilectl share toggle`. `status --json` inclui `output_sharing` e
`output_sharing_restore_pending`. A Top Bar indica **COMPARTILHANDO** enquanto ativo.
Nenhum atalho existente é remapeado.

## Condições e comportamento

- Captura toda a saída do destino selecionado, incluindo notificações e a voz dos
  participantes. O retorno digital da chamada é uma limitação aceita deste modo.
- Abra a chamada antes de ativar. É necessário haver exatamente um stream externo
  de captura de microfone ativo. Capturas múltiplas/ambíguas são recusadas.
- A operação não muda a entrada padrão global, volumes, mutes físicos/virtuais ou
  presets. O áudio é copiado do monitor da saída física, após o DSP de saída.
- Desligue a escuta do microfone antes de compartilhar. Os dois modos são
  mutuamente exclusivos para evitar retorno da própria voz.
- O botão de mute do microfone encerra o compartilhamento e aplica dual-mute.
  Pânico encerra o compartilhamento, aplica os mutes de emergência e nunca volta
  a compartilhar ao sair do pânico. O snapshot mantém seus três booleans.
- Reset e seleção de destino encerram o compartilhamento antes de continuar.
- O modo não é persistido. O transporte tem duração máxima de dez minutos e é
  encerrado na saída normal ou morte do daemon. A reconciliação por eventos conclui
  a restauração após a expiração. Não há polling periódico novo.
- Se o stream desaparecer, IDs reutilizados não são alterados. Se alguém trocar
  manualmente seu alvo de captura, a escolha externa é preservada.
- Se a entrada original sumir, o transporte é encerrado e o status informa
  restauração pendente; desligar novamente tenta concluir. A retomada acústica após
  morte abrupta do daemon depende também da política do WirePlumber e deve ser
  homologada: não existe promessa de rollback executado em SIGKILL.
- Meet/WhatsApp podem filtrar música como ruído. A compatibilidade real depende
  da sessão, navegador e conta; testes fake não a certificam.

## Implementação e isolamento

Session D-Bus continua sendo o único IPC entre CLI, daemon e extensão. Métodos:
`SetOutputSharing(bool)` e `ToggleOutputSharing()`, ambos retornando o estado ativo.
O worker serial aplica uma transação sobre `pw-loopback` e o alvo por-stream do
WirePlumber via `pw-metadata`. A ordem dos argumentos do metadata é valor/tipo.
Readbacks verificam alimentação do monitor e exclusividade da entrada substituída.

O processo filho tem deadline, encerramento supervisionado e `Pdeathsig`; nenhum
arquivo de configuração PipeWire/EasyEffects é escrito. A restauração conserva
presença/ausência, tipo e valor anteriores de `target.object`. Falhas de ativação
tentam rollback com contexto independente do cancelamento original. O pânico
encerra o transporte em paralelo aos mutes, sem esperar restauração da captura
para silenciar os alvos existentes.

Testes automatizados usam `EASYPROFILE_TEST_MODE=1`, adaptadores fake e grafo em
memória; as entradas públicas do adaptador real recusam execução. O harness D-Bus
também bloqueia `pw-loopback` e `pw-link` no PATH.

## Homologação humana após carregar a nova versão

1. Confira a presença de `share` na ajuda da CLI e do botão no menu.
2. Com uma única chamada aberta e escuta do microfone desligada, reproduza um
   trecho curto. Ative o botão: confirme que a outra pessoa recebe o trecho e
   você continua ouvindo. Sua voz não deve se misturar ao compartilhamento.
3. Desative: confirme retomada do microfone anterior, com os mesmos mutes e níveis.
4. Repita com o microfone previamente mutado: ativar/desativar não pode desmutá-lo.
5. Ative novamente e teste mute do mic, pânico, saída do pânico, Reset e mudança
   de destino. Compartilhamento não pode voltar sozinho.
6. Feche a chamada durante o compartilhamento. Verifique desligamento por
   reconciliação e ausência de captura errada ao abrir outra chamada.
7. Em sessão acompanhada, teste expiração e reinício do daemon; verifique status,
   ausência de transporte órfão, restauração e hashes dos presets.

## Estado do host observado nesta entrega

O checkout inicial estava limpo em `b624124`. O ROADMAP local registra cutover
concluído: guards legados inativos e `easyprofiled.service` ativo. Esse estado foi
preservado. O gate integral ainda exige ambos os guards legados ativos; essa
divergência preexistente impede declarar os seis gates verdes. Não reativar guards
ou alterar o critério silenciosamente para contornar esse resultado.

Os presets de saída do baseline foram renomeados externamente para
`AKG K52 - Padrão.json` e `SP166 - Padrão.json`.
Seu SHA-256 continua `7802bfbf95d5fe70d1964e6cfca63adc90a86ed26d78837dd45b2f2a3a142f52`;
KA-1333 e AKG também coincidem com o baseline original. O verificador por caminho
antigo falha, embora os três conteúdos estejam preservados. Nenhum preset ou
baseline foi reescrito para contornar essa divergência.

## Resultado da validação de engenharia

- Formatação, vet, staticcheck, build com race, auditoria AST e higiene Git: passaram.
- `EASYPROFILE_TEST_MODE=1 go test -race ./...`: passou, incluindo transações sobre
  grafo fake, rollback após aplicação parcial, cancelamento, morte do transporte,
  identidade reutilizada, alteração manual, readback e regras de pânico/mute.
- Seis testes de cutover com ferramentas falsas e regressões da extensão com
  atores em memória: passaram.
- CLI/daemon/proxy GJS real em barramento privado: passaram, incluindo os métodos
  novos e ciclos on/off/toggle; zero comandos de áudio do host.
- Parser: 3,73 ms/op, abaixo de 5 ms.
- Desempenho fake em 60 s: CPU 0%, RSS após CLI 9.920 KiB; p95 CLI 12,91 ms,
  máximo 15,48 ms. A primeira medição apresentou p95 32,27 ms; a repetição sem as
  suítes concorrentes passou. Isso não mede áudio real ou recursos do GNOME.
- O gate oficial integral parou na etapa 3 pelas divergências ambientais descritas
  acima. As demais verificações foram executadas separadamente; não se declara
  aprovação integral dos seis gates.

Binário e extensão foram instalados nos caminhos existentes do usuário, com backup
em `/tmp/easyprofile-sharing-install-t55hdrb_`. Hash do binário e bytes da extensão,
proxy, metadata e schema compilado foram conferidos. O PID do daemon permaneceu
igual durante a instalação: a nova versão será carregada no próximo login gráfico.
Não houve restart de serviço nem ativação da feature em áudio real nesta entrega.

## Atalho Numpad 8

Com NumLock no modo áudio, `8` chama `ToggleOutputSharing`, com
`IGNORE_AUTOREPEAT`. O botão do menu e `share on|off|toggle` continuam disponíveis.
O mesmo toggle encerra uma sessão ativa ou tenta concluir uma restauração pendente.
