"use client";

import { useState, type PointerEvent } from "react";
import { Button, Dialog } from "@inspection/design-system";

type Props = {
  referenceURL: string;
  photoURL: string;
  referenceLabel: string;
  onReferenceError: () => void;
};

export function PhotoComparison({ referenceURL, photoURL, referenceLabel, onReferenceError }: Props) {
  const [split, setSplit] = useState(50);
  const [expanded, setExpanded] = useState(false);

  const moveTo = (event: PointerEvent<HTMLDivElement>) => {
    const bounds = event.currentTarget.getBoundingClientRect();
    if (bounds.width > 0) setSplit(Math.round(Math.min(100, Math.max(0, ((event.clientX - bounds.left) / bounds.width) * 100))));
  };

  return <div className="capture-photo-comparison">
    <div className="capture-photo-comparison__frame" onPointerDown={(event) => { event.currentTarget.setPointerCapture(event.pointerId); moveTo(event); }} onPointerMove={(event) => { if (event.currentTarget.hasPointerCapture(event.pointerId)) moveTo(event); }}>
      <img src={referenceURL} alt={`Foto de referência: ${referenceLabel}`} onError={onReferenceError} />
      <img className="capture-photo-comparison__current" src={photoURL} alt="Prévia da sua foto de comparação" style={{ clipPath: `inset(0 0 0 ${split}%)` }} />
      <span className="capture-reference-badge" aria-hidden="true">Referência</span>
      <span className="capture-reference-badge capture-reference-badge--current" aria-hidden="true">Sua foto</span>
      <span className="capture-photo-comparison__divider" style={{ left: `${split}%` }} aria-hidden="true"><span>↔</span></span>
      <input type="range" min="0" max="100" value={split} onChange={(event) => setSplit(Number(event.target.value))} aria-label="Comparar referência e sua foto" aria-valuetext={`${split}% referência, ${100 - split}% sua foto`} />
    </div>
    <div className="capture-photo-comparison__tools"><p>↔ Arraste para comparar.</p><Button variant="text" onClick={() => setExpanded(true)}>Ver fotos completas</Button></div>
    {expanded && <Dialog isOpen onClose={() => setExpanded(false)} size="wide" title="Fotos completas"><div className="capture-image-overlay"><p>Referência · {referenceLabel}</p><img src={referenceURL} alt="Referência completa" /><p>Sua foto</p><img src={photoURL} alt="Sua foto completa" /></div></Dialog>}
  </div>;
}
