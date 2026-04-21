

Esta feature almeja aumentar a precisao dos recnhecimento de musica em CD e vinyl, principalmente albuns gaoless (e.g. ao vivo, the dark side of the moon, etc).

Na parte de configuracao de reconhecimento deve ser adicionado dois wizards:

1. Microphone calibration: hoje o micrfone esta muito bem calibrado. A calibragem se resume a ajuste de ganho ou volume. Pode ser usado os valores atuais como padrao. Uma vez definido, o valor eh atualizado no sistema. Se nao for possivel alteracao o raspberry pi com os valores eh feita a recomendacao ao usuario com os comandos para execucao apropriados.
2. Silence calibration: Esta tem impacto directo para reconhecimento de transicoes ou passagens silenciosas em musicas. Ele tambem pode ser util para checar se o amplificador esta ligado. Neste wizar usamos os inputs configurados como visíveis para o usuario para efetuar a calibragem. É solitado para que o usuario selecione o input e inicie o wizard. No wizar é solitado para que o usuário desligue o CD (ou Phono) e o sistema captura o RMS e armazena uma media. Depois, pede para que o usuário ligue o CD (ou Phono) e o sistema captura o RMS e armazena a media. Com isso, poderá ser possível idenficar coisas do tipo: a) passagens silenciosas mas nao tao silencioas a ponto de ser igual ao phono stage desligado. b) usuario levantou a agulha do disco.
Um passo exclusivo para vinyl ppode pedir para que o usuário coloque no inicio do LP para capturarmos o som inicial. Depois, solita para que o usuário coloque a agulha bem no final de uma faixa que tenha trasicao silenciosa, ai capturamos o final da musica, o silencio, e o inicio da musica. Amazena-se os dados relevantes.


Tendo os dados armanzenados, eles ficam disponível para os reconhecimentos.

Se eu estiver esquecendo algo me avise. Como voce eh especialista, conheciedor de usica e engeneheiro de som, pode sugerir coisas para essa feature tambem.