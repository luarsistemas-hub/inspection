"use client";

import React, { useRef, useState } from "react";
import { Button, Field, Input, Select } from "@inspection/design-system";
import { ENVIRONMENT_TYPES, environmentNameForType, isDuplicateEnvironment, normalizeEnvironmentName, validateEnvironmentName } from "@/pwa/environment-flow";

export function EnvironmentDeclaration({ busy, existing = [], onDeclare }: { busy: boolean; existing?: Array<{ typeCode: string | null; name: string }>; onDeclare: (typeCode: string, name: string) => Promise<string | undefined> }) {
  const [typeCode, setTypeCode] = useState("LIVING_ROOM");
  const [name, setName] = useState("");
  const [nameTouched, setNameTouched] = useState(false);
  const editRevision = useRef(0);
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
  };
  return <section className="capture-environment-add" aria-labelledby="capture-environment-add-title">
    <h3 id="capture-environment-add-title">Adicionar ambiente</h3>
    <Field label="Tipo de ambiente"><Select aria-label="Tipo de ambiente" value={typeCode} onChange={(event) => { editRevision.current += 1; setTypeCode(event.target.value); setNameTouched(true); }}>{ENVIRONMENT_TYPES.map(([code, label]) => <option key={code} value={code}>{label}</option>)}</Select></Field>
    <Field label={typeCode === "OTHER" ? "Nome do ambiente" : "Nome (opcional)"} hint={error}><Input aria-label="Nome do ambiente" value={name} onChange={(event) => { editRevision.current += 1; setName(event.target.value); setNameTouched(true); }} maxLength={160} aria-invalid={Boolean(error)} /></Field>
    <Button disabled={busy || Boolean(error)} isPending={busy} onClick={() => void declare()}>Continuar</Button>
  </section>;
}
