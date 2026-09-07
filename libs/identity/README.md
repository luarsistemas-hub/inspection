# Identity

`libs/identity` contém o tipo UUID compartilhado, geração de IDs e parsing usado pelos slices e pelo banco. Use `identity.NewID`, `identity.ParseID` e `identity.NewDeterministicID`; não replique conversões UUID nos serviços.

```sh
go test ./libs/identity
```
