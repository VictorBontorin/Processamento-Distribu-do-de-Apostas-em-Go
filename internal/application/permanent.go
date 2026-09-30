package application

import "errors"

// ErrPermanent marca falhas que nenhuma nova tentativa resolve (mensagem
// malformada, conflito de conteúdo). O consumidor SQS envia essas mensagens
// direto para a DLQ; as demais falhas são transitórias e seguem em retry.
var ErrPermanent = errors.New("permanent failure")
