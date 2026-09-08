# [P2] Atualizar a asserção do smoke para o cache atual do Service Worker

## Evidência

`./scripts/smoke.sh` falha após health/readiness porque procura
`inspection-static-v1` em `http://localhost:3003/sw.js`. O arquivo servido pelo
Capture declara `inspection-capture-shell-v1`. Depois dessa asserção, o mesmo
script também usa `curl -fsSI http://localhost:3003/`; a raiz do Capture é uma
rota legítima 404 porque o produto só expõe `/capture/[linkToken]`, embora os
headers estejam presentes.

## Resultado esperado

O smoke deve validar o identificador vigente do shell PWA, ou preferencialmente
uma propriedade estável do Service Worker sem duplicar um detalhe de versão que
precisa ser alterado junto com o código.

## Critérios de aceite

- O smoke passa com o Service Worker atualmente servido.
- A checagem continua garantindo que `sw.js` e `manifest.webmanifest` estão
  acessíveis.
- A atualização não mascara falhas de headers de segurança e usa uma rota
  existente para o check do Capture.
