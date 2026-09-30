import { createRoot } from "react-dom/client";
import { useRef, useState } from "react";
import { Button, Combobox, Dialog, Field, InfoDisclosure } from "../../dist/index.js";
import "../../dist/styles.css";

function Harness() {
  const [isOpen, setIsOpen] = useState(false);
  const [asset, setAsset] = useState("");
  const triggerRef = useRef<HTMLButtonElement>(null);
  return <>
    <InfoDisclosure label="vistoria" heading={<h1>Nova vistoria</h1>}>A alteração será confirmada pelo servidor.</InfoDisclosure>
    <Button ref={triggerRef} onClick={() => setIsOpen(true)}>Nova vistoria</Button>
    <p data-testid="selection">{asset || "Nenhum imóvel selecionado"}</p>
    <Dialog isOpen={isOpen} onClose={() => setIsOpen(false)} restoreFocusRef={triggerRef} size="wide" title="Nova vistoria">
      <Field label="Imóvel" error={asset ? undefined : "Selecione um imóvel"}>
        <Combobox onChange={setAsset} options={[{ value: "assetA", label: "Apartamento 101" }]} value={asset} />
      </Field>
      <Button onClick={() => setIsOpen(false)}>Fechar</Button>
    </Dialog>
  </>;
}

createRoot(document.getElementById("root")!).render(<Harness />);
