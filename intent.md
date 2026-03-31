Intencoes com o projeto.

0) Vou escrever no chat em portugues mas todo o codigo e documentacao precisa ser em ingles. Me avise se a criacao de algum arquivo MD ajudaria com essas definicoes (e.g. claude.md, skills etc)
1) Ser o backend para prover informacoes de audio para um outro program que fara a apresentacao. Por exemplo, atualmente este programa habilita o airplay de forma que o UI le os dados da musica e das capas dos discos.
2) Pretende habilitar no futuro bluetooth e upnpn. O ideal era ter uma uma unica forma do UI obter informacoes de faixa e album independent da forma de stream que esta sendo feita.
3) Tambem pretendo habilitar a identifiacao de faixas que estao tocando em mideias fiscas por meio de um microfone captando diretaente de saida de linha fixa de amplificador. Essas dados tambem fariam parte da abstracao que o UI veria. Aqui eteria mecnismo de deteccao d musica atuando.
4) O UI precisaria saber de esta tocando AirPlay, BlueTooth, Upnp ou media fisica.
5) O UI teraa opcao para mostrar differentes interfaces including uma que mostraria o VU.

Para esses requisitos imagino que teria que mudar a abordagem aqui.
Imagino que exista uma forma profissional de fazer isso no Linux/raspberry pi sem que um processo bloqueie o outro. A solução padrão para VU em tempo real + detecção + gravação usando alguma forma de stream (ouvi falar sobre pipewire ou pulse audio mas nao sei bem do que se trata isso.).

O que voce sugere de arquitetura e design para isso?

Mais uma coisa. O mecanismo de deteccao de CD ou Vinyl esta falho. Precisamos focar la antes de deixar isso disponivel como funcionalidade. Vamos deixar isso para o futro. Agora o que podemos fazer eh quando detectarmos que ha som vindo pelo saida REC-OUT e entrado no PI via USB (microphone) mostramos apenas "Physical media". Importante, temos que entender se o airplay esta ativo ou nao (ou bluetooth ou upnpn no futuro) pois se estiver nao precisamos ler som do microfone. Me enganei, deixa eu revisar, o output ainda precisa ser verificado, pois queremos a funcionalidade de VU. Somente a identificacao da musica que nao sera necessaria pois isso ja acontece com o airplay e os outros.


Agora sobre armazenar dados das medias fisicas. Como eu tenho uma colecao que nao eh grande isso pode ser feito com facilidade eu acho.
Pode ficar meio caotico pois as vezes eu escuto somente o lado A de um disco. Outra hora escuto um cd completo, outra hora escuto o disco todo. Existe um desafio grande em capturar dados do audio e fazer sentido sobre o algum (primeira faixa, etc). Pode ficar bem complicado tipo, detectar que a faixa eh a primeira do album e ja baixar os dados das demais faixas. 

Se existir uma for elagante de fazer isso seria legal.
Poderia criar uma base local que fosse possivel de exportar via interface web e de, de fato, editar e corrigir via interface web (tipo detalhes da sequecia musicas e a capa correta do album).

Outro desafio seria, uma vez armazendo os dados, como buscar baseado no som? Vejo vantagens para a capa, por exemplo, busca dados da musica e o reconhecimento da capa deixa de ser necessario se em cache.

O que voce acha de tudo isso?