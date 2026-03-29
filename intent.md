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

Mais uma coisa. O mecanismo de deteccao de CD ou Vinyl esta falho. Precisamos focar la antes de deixar isso disponivel como funcionalidade. Vamos deixar isso para o futro. Agora o que podemos fazer eh quando detectarmos que ha som vindo pelo saida REC-OUT e entrado no PI via USB (microphone) mostramos apenas "Physical media". Importante, temos que entender se o airplay esta ativo ou nao (ou bluetooth ou upnpn no futuro) pois se estiver nao precisamos ler som do microfone.