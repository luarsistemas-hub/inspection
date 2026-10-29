"use client";

import React, { useRef, useState } from "react";
import { Button, Field, Input, Select } from "@inspection/design-system";
import { ENVIRONMENT_TYPES, environmentNameForType, isDuplicateEnvironment, normalizeEnvironmentName, validateEnvironmentName } from "@/pwa/environment-flow";
import { EnvironmentTypeIcon } from "./environment-type-icon";

const featuredTypes = ["LIVING_ROOM", "KITCHEN", "BEDROOM", "BATHROOM", "SERVICE_AREA", "OTHER"];

export function EnvironmentDeclaration({ busy, existing = [], onDeclare, layout = "select" }: { busy: boolean; existing?: Array<{ typeCode: string | null; name: string }>; onDeclare: (typeCode: string, name: string) => Promise<string | undefined>; layout?: "select" | "tiles" }) {
  const [typeCode, setTypeCode] = useState("LIVING_ROOM");
  const [name, setName] = useState("");
  const [nameTouched, setNameTouched] = useState(false);
  const editRevision = useRef(0);
  const nameDisclosure = useRef<HTMLDetailsElement>(null);
  const validationError = validateEnvironmentName(typeCode, name);
  const duplicateError = isDuplicateEnvironment(typeCode, environmentNameForType(typeCode, name), existing) ? "Esse ambiente já existe. Diferencie o nome, por exemplo: Quarto 2." : undefined;
  const error = nameTouched ? validationError ?? duplicateError : undefined;
  const declare = async () => {
    if (busy || error) return;
    const submittedType = typeCode;
    const submittedName = name;
    const submittedRevision = editRevision.current;
    setName("");
    setNameTouched(false);
    const submittedNameResolved = environmentNameForType(submittedType, submittedName);
    const result = await onDeclare(submittedType, submittedNameResolved);
    if (editRevision.current !== submittedRevision) return;
    if (result === undefined) {
      setName(submittedName);
      setNameTouched(true);
      return;
    }
    const used = [...existing, { typeCode: submittedType, name: submittedNameResolved }];
    const nextType = ENVIRONMENT_TYPES.find(([code, label]) => !used.some((item) => item.typeCode === code && normalizeEnvironmentName(item.name) === normalizeEnvironmentName(label)))?.[0];
    if (nextType) setTypeCode(nextType);
    setName("");
    setNameTouched(false);
    if (nameDisclosure.current) nameDisclosure.current.open = false;
  };
  const nameField = <Field label={typeCode === "OTHER" ? "Nome do ambiente" : "Personalizar nome (opcional)"} hint={error}><Input aria-label="Nome do ambiente" value={name} onChange={(event) => { editRevision.current += 1; setName(event.target.value); setNameTouched(true); }} maxLength={80} aria-invalid={Boolean(error)} placeholder={typeCode === "OTHER" ? "Ex.: Varanda" : "Ex.: Quarto do casal"} /></Field>;
  return <section className={`capture-environment-add ${layout === "tiles" ? "capture-environment-add--tiles" : ""}`} aria-labelledby="capture-environment-add-title">
    <h3 id="capture-environment-add-title">Adicionar ambiente</h3>
    {layout === "tiles" ? <><div className="capture-environment-types" role="group" aria-label="Tipo de ambiente">{featuredTypes.map((code) => <button key={code} type="button" className="capture-environment-type" aria-pressed={typeCode === code} onClick={() => { editRevision.current += 1; setTypeCode(code); setName(""); setNameTouched(code === "OTHER"); }}><EnvironmentTypeIcon typeCode={code} /><span>{ENVIRONMENT_TYPES.find(([value]) => value === code)?.[1]}</span></button>)}</div><details className="capture-environment-more"><summary>Mais tipos de ambiente</summary><Field label="Tipo de ambiente"><Select aria-label="Outros tipos de ambiente" value={featuredTypes.includes(typeCode) ? "" : typeCode} onChange={(event) => { editRevision.current += 1; setTypeCode(event.target.value); setName(""); setNameTouched(false); }}><option value="" disabled>Escolha outro tipo</option>{ENVIRONMENT_TYPES.filter(([code]) => !featuredTypes.includes(code)).map(([code, label]) => <option key={code} value={code}>{label}</option>)}</Select></Field></details></> : <Field label="Qual ambiente você vai gravar?"><Select aria-label="Tipo de ambiente" value={typeCode} onChange={(event) => { editRevision.current += 1; setTypeCode(event.target.value); setNameTouched(true); }}>{ENVIRONMENT_TYPES.map(([code, label]) => <option key={code} value={code}>{label}</option>)}</Select></Field>}
    {layout === "tiles" && typeCode !== "OTHER" ? <details ref={nameDisclosure} className="capture-environment-name"><summary>Personalizar nome <span>(opcional)</span></summary>{nameField}</details> : nameField}
    <Button disabled={busy || Boolean(error)} isPending={busy} onClick={() => void declare()}>{layout === "tiles" ? "Adicionar ambiente" : "Continuar"}</Button>
    {layout === "tiles" && <small className="capture-environment-add-hint">Você poderá renomear ou remover depois.</small>}
  </section>;
}
