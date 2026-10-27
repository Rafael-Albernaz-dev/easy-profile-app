# Compartilhar toda a saída em uma chamada

O menu do EasyProfile oferece **Compartilhar toda a saída na chamada**.
Com uma chamada já capturando o microfone, ligar o botão substitui temporariamente
essa entrada pelo áudio da saída física selecionada. Desligar encerra o transporte
e restaura a política de entrada anterior. O áudio continua chegando ao fone/caixas.
Não há mistura com a voz do microfone.

CLI equivalente: `easyprofilectl share on`, `easyprofilectl share off` ou
`easyprofilectl share toggle`. `status --json` inclui `output_sharing` e
`output_sharing_restore_pending`. A Top Bar indica **COMPARTILHANDO** enquanto ativo.
Numpad8 alterna o modo com NumLock no modo áudio.

## Condições e comportamento

- Captura toda a saída do destino selecionado, incluindo notificações e a voz dos
  participantes. O retorno digital da chamada é uma limitação aceita deste modo.
- Abra a chamada antes de ativar. É necessário haver exatamente um stream externo
  de captura de microfone ativo. O seletor diagnostica granularmente o estado da topologia
  e emite instruções claras em português:
  - Nenhuma captura: *nenhuma chamada capturando o microfone foi encontrada; abra a chamada antes de compartilhar*
  - Captura pausada (não comprova mute no aplicativo): *a chamada foi detectada, mas sua captura está pausada; ative a entrada de áudio na chamada e tente novamente*
  - Captura não conectada: *captura da chamada ainda não conectada ao áudio do sistema; aguarde a inicialização da chamada*
  - Múltiplas capturas/ambiguidade: *múltiplas capturas de áudio detectadas; mantenha apenas a chamada desejada capturando o microfone*
  - Fonte não reconhecida: *captura conectada a uma fonte não reconhecida; certifique-se de que a chamada está vinculada ao microfone*
  - Permissões/serial ausente: *identificador da captura indisponível ou permissões insuficientes no PipeWire*
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

## Diagnóstico atual do WhatsApp e inicialização

A falha de recepção relatada no WhatsApp **não tem causa confirmada**. Um grafo
sem captura em outro instante não explica a chamada anterior; links pausados não
provam que o usuário estava mutado. Filtros WebRTC também são hipótese, sem prova
nesta sessão. O teste acompanhado deve registrar a captura/serial, seus links,
metadata target.object e recepção remota antes/durante/depois do toggle.

A inicialização agora espera a fonte virtual existir, redireciona somente a captura
selecionada usando seu destino por serial e só aceita ON depois de confirmar
monitor físico -> loopback ativo e fonte virtual -> captura ativa/exclusiva.
Um feed passivo pausado não bloqueia a descoberta inicial, nem serve de prova
final de transporte. Captura e fonte negociam o mesmo formato MONO, com remix
habilitado na captura para combinar FL/FR do monitor físico antes do loopback.
No [PipeWire 1.0.5](https://github.com/PipeWire/pipewire/blob/1.0.5/src/modules/module-loopback.c#L245-L289),
o loopback copia os canais por índice; pedir FL/FR na captura e MONO na fonte
descartava FR em vez de fazer downmix. Esse defeito foi identificado na revisão
do código; não comprova a causa do silêncio total relatado no WhatsApp.
O teste hermético verifica os formatos solicitados e as proteções de rota,
sem executar áudio real. Negociação, ambos os canais e recepção remota ainda
precisam de validação acompanhada.

Ausência de confirmação publica output_sharing=false e restauração pendente se
houver transporte/política a limpar. Não há seleção automática de novos apps.
A restauração preserva o metadata anterior exato e não toca IDs reutilizados.
PTT também é incompatível com compartilhamento. O indicador prova uma rota de
transporte confirmada, não recepção do interlocutor nem conteúdo audível.

## Reteste acompanhado no WhatsApp — 20/10/2026

O binário com captura e fonte MONO foi instalado (`68ca22dc…`). O usuário
confirmou voz normal antes de ligar `8`; com compartilhamento ligado, ele ouvia
o áudio do computador, mas o interlocutor recebia silêncio. A homologação
continua **reprovada**, apesar de `output_sharing=true` e dos links ativos.

O snapshot confirmou monitor físico -> loopback e fonte virtual -> captura
Chrome ativa e exclusiva. Medição curta autorizada encontrou sinal não nulo
no monitor e na fonte virtual, sem guardar gravação. As amostras cobrem somente
0,192s/0,373s; não comprovam continuidade, processamento interno do navegador
ou entrega remota. Cancelamento de eco/redução de ruído seguem hipóteses.

Ao usuário fechar a chamada, a reconciliação encerrou o transporte e removeu
a fonte temporária: compartilhamento e restauração pendente ficaram desligados,
com os três mutes preservados. Não havia capturas externas no snapshot final.
Comparação do mesmo trecho falado em player local e navegador ainda está pendente.
O contrato foi reconfirmado: somente áudio do computador, sem misturar microfone.

## Reteste com VLC e limitação aceita pelo usuário

Em uma segunda chamada, o usuário confirmou que o interlocutor ouviu os áudios
reproduzidos no VLC. A reprodução no Chrome não chegou, mesmo com WhatsApp e
reprodução em perfis diferentes do mesmo Chrome. Isso valida recepção pelo
caminho do VLC nessa chamada, sem certificar todos os players ou navegadores.
O observador de topologia não cobriu o trecho VLC; não se declara comparação
simultânea de rotas ou uma causa confirmada de cancelamento de eco.

O usuário optou por manter a implementação atual e usar reprodução local quando
necessário. Compatibilidade com áudio do Chrome fica como limitação conhecida,
com investigação adiada. A feature continua capturando o monitor completo;
não há filtro que selecione apenas VLC e não se mistura voz do microfone.
Baixar um arquivo e reproduzi-lo no VLC é uma alternativa coerente com o teste,
sem garantia universal para qualquer arquivo/player/sessão.

Continuam pendentes em hardware as provas de restauração ao desligar `8` com a
chamada ainda aberta e de interrupção por mute/pânico/Reset/troca de saída.
O encerramento automático quando a chamada desapareceu foi observado.
