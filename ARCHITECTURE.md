# Architecture

### Moedas suportadas

Nesta implementação, as moedas aceitas são:

- BRL
- USD
- EUR

O domínio rejeita moedas que não estejam nessa lista.

A estrutura foi feita de forma que novas moedas possam ser adicionadas posteriormente.

### Formato monetário

A implementação exige exatamente duas casas decimais para valores monetários.

Exemplos aceitos:

- "25.00"
- "10.50"
- "0.01"

Exemplos rejeitados:

- "25"
- "25.5"
- "25.000"

### Valores por tipo de operação

As regras específicas de valor por tipo de operação serão aplicadas
na entidade WagerTransaction.

Atualmente essa validação ainda não está implementada no domínio de
WagerTransaction.