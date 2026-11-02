const pages = {
  inicio: "Operação / Início",
  captura: "Captura do participante (PWA) / Checklist",
  preferencias: "Configuração / Preferências da IA",
  modelo: "Configuração / Modelo de laudo",
  laudo: "Operação / Laudo descritivo",
  comparacao: "Operação / Comparação",
  imovel: "Portfólio / Ficha do imóvel",
  ajuda: "Captura do participante (PWA) / Ajuda"
};

const toast = document.getElementById("toast");
let toastTimer;
function showToast(message) {
  toast.textContent = message;
  toast.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { toast.hidden = true; }, 3600);
}

function showPage() {
  const page = Object.hasOwn(pages, location.hash.slice(1)) ? location.hash.slice(1) : "inicio";
  document.querySelectorAll("[data-page]").forEach(section => {
    section.hidden = section.dataset.page !== page;
  });
  document.querySelectorAll("[data-page-link]").forEach(link => {
    if (link.dataset.pageLink === page) link.setAttribute("aria-current", "page");
    else link.removeAttribute("aria-current");
  });
  document.getElementById("topbar-path").textContent = pages[page];
  document.querySelector(".sidebar").classList.remove("is-open");
  document.title = `Inspection · ${pages[page].split(" / ").pop()} · Protótipo`;
  window.scrollTo({ top: 0, behavior: "instant" });
}
window.addEventListener("hashchange", showPage);
showPage();
document.querySelectorAll("[data-go]").forEach(button => {
  button.addEventListener("click", () => { location.hash = button.dataset.go; });
});
document.querySelector(".mobile-menu").addEventListener("click", () => {
  document.querySelector(".sidebar").classList.toggle("is-open");
});
document.addEventListener("keydown", event => {
  if (event.key === "Escape") document.querySelector(".sidebar").classList.remove("is-open");
});

let ceilingCaptured = false;
function markCeiling() {
  if (ceilingCaptured) return;
  ceilingCaptured = true;
  const component = document.getElementById("ceiling-component");
  component.classList.remove("attention");
  component.querySelector(".state-icon").className = "state-icon success";
  component.querySelector(".state-icon").textContent = "✓";
  component.querySelector("p").textContent = "Nova vista ilustrativa registrada; análise visual pendente de revisão.";
  component.querySelector("button").replaceWith(Object.assign(document.createElement("span"), {
    className: "tag blue",
    textContent: "Aguardando revisão"
  }));
  document.getElementById("coverage-label").textContent = "7 de 8 componentes com evidência";
  document.getElementById("coverage-note").textContent = "Comparáveis na saída: 7 de 8. Cobertura e conservação são medidas diferentes.";
  document.getElementById("coverage-fill").style.width = "88%";
  document.getElementById("room-kitchen-count").textContent = "7 de 8 componentes com evidência";
  document.querySelector('#mobile-room option[value="cozinha"]').textContent = "01 · Cozinha · 7/8 com evidência";
  showToast("Vista demonstrativa do teto adicionada. O piso continua sem vista.");
}
document.getElementById("add-photo").addEventListener("click", markCeiling);
document.getElementById("mark-ceiling").addEventListener("click", markCeiling);
document.getElementById("capture-next").addEventListener("click", () => {
  location.hash = "laudo";
  showToast("Ambiente concluído com lacunas registradas. Abrindo a prévia que o gestor verá no painel.");
});
document.getElementById("voice-button").addEventListener("click", event => {
  event.currentTarget.textContent = "Declaração registrada";
  showToast("Declaração fictícia registrada separadamente da evidência visual.");
});
document.querySelectorAll(".room:not(.selected)").forEach(button => {
  button.addEventListener("click", () => {
    showToast("Este protótipo apresenta a cozinha em detalhe. Os demais ambientes são exemplos de navegação.");
  });
});
document.getElementById("mobile-room").addEventListener("change", event => {
  if (event.currentTarget.value !== "cozinha") {
    showToast("Este protótipo apresenta a cozinha em detalhe. Os demais ambientes são exemplos de navegação.");
    event.currentTarget.value = "cozinha";
  }
});

let format = "topicos";
function renderPreview() {
  const detail = document.getElementById("detail-level").value;
  const tone = document.getElementById("tone").value;
  const target = document.getElementById("ai-preview");
  let points;
  if (detail === "curto") points = [
    "Paredes, armários, bancada, pia e instalações aparentes estão visíveis na foto F01.",
    "Teto e piso estão sem vista; o interior dos armários não foi avaliado."
  ];
  else if (detail === "intermediario") points = [
    "Paredes com revestimento claro; armários superiores e inferiores com frentes verde-oliva e acabamento amadeirado nas faces vistas (F01).",
    "Bancada, pia, torneira, cooktop, depurador e tomadas aparentes estão visíveis; partes da superfície estão encobertas (F01).",
    "Teto e piso sem vista; interior dos armários e funcionamento dos equipamentos não avaliados."
  ];
  else points = [
    "Paredes e revestimento: revestimento claro com veios, sem dano evidente na região enquadrada (F01).",
    "Armários superiores: frentes verde-oliva e amadeiradas, sem dano evidente nas faces enquadradas (F01).",
    "Armários inferiores: portas visíveis; interior e ferragens não avaliados (F01).",
    "Bancada, pia e torneira: acabamento amadeirado, com objetos que encobrem parte da superfície (F01).",
    "Cooktop e depurador: identificáveis; tipo de aquecimento e funcionamento não confirmados (F01).",
    "Pontos elétricos: tomadas aparentes; funcionamento não testado (F01).",
    "Cobertura: teto e piso ainda sem vista suficiente."
  ];
  if (tone === "simples") points = points.map(point => point
    .replace("faces enquadradas", "partes que aparecem na foto")
    .replace("ferragens", "peças internas")
    .replace("acabamento amadeirado", "aparência de madeira")
    .replace("aparentes", "visíveis")
    .replace("sem vista suficiente", "sem foto suficiente"));
  if (format === "topicos") {
    const list = document.createElement("ul");
    points.forEach(point => {
      const item = document.createElement("li");
      item.textContent = point;
      list.append(item);
    });
    target.replaceChildren(list);
  } else {
    const paragraph = document.createElement("p");
    paragraph.textContent = points.join(" ");
    target.replaceChildren(paragraph);
  }
}
document.getElementById("detail-level").addEventListener("change", renderPreview);
document.getElementById("tone").addEventListener("change", renderPreview);
document.querySelectorAll("[data-format]").forEach(button => {
  button.addEventListener("click", () => {
    format = button.dataset.format;
    document.querySelectorAll("[data-format]").forEach(item => item.classList.toggle("active", item === button));
    renderPreview();
  });
});
document.querySelectorAll("[name=scope]").forEach(input => {
  input.addEventListener("change", () => {
    document.querySelectorAll(".choice").forEach(label => label.classList.toggle("selected", label.contains(input) && input.checked));
  });
});
document.getElementById("save-preferences").addEventListener("click", () => {
  showToast("Preferências salvas nesta prévia demonstrativa.");
});
renderPreview();

document.getElementById("model-title").addEventListener("input", event => {
  document.getElementById("model-preview-title").textContent = event.currentTarget.value.trim() || "Laudo de vistoria";
});
document.querySelectorAll("[data-color]").forEach(button => {
  button.addEventListener("click", () => {
    document.querySelectorAll("[data-color]").forEach(item => item.classList.toggle("active", item === button));
    document.querySelector(".model-preview").style.setProperty("--model-accent", button.dataset.color);
  });
});
document.querySelectorAll("[name=report-layout-choice]").forEach(input => {
  input.addEventListener("change", () => {
    document.querySelectorAll("[name=report-layout-choice]").forEach(item => {
      item.closest(".choice").classList.toggle("selected", item.checked);
    });
    document.querySelector(".model-preview").classList.toggle("annex-layout", input.value === "resumo" && input.checked);
  });
});
document.getElementById("change-logo").addEventListener("click", () => {
  showToast("Nesta prévia, o logo é demonstrativo. A implementação deve validar formato, tamanho e contraste.");
});
document.getElementById("save-model").addEventListener("click", () => {
  showToast("Modelo salvo nesta prévia demonstrativa.");
});

document.getElementById("report-layout").addEventListener("click", event => {
  const sheet = document.querySelector(".report-sheet");
  const compact = sheet.classList.toggle("is-summary");
  event.currentTarget.textContent = compact ? "Ver versão completa" : "Ver versão resumida";
  showToast(compact ? "Resumo visual ativado; limitações e evidências permanecem no documento." : "Versão completa ativada.");
});
document.getElementById("report-publish").addEventListener("click", () => {
  showToast("Publicação bloqueada nesta prévia: teto e piso da cozinha estão sem vista e 2 observações aguardam revisão. Capture as vistas ou registre o motivo antes de publicar.");
});

document.querySelectorAll("[data-filter]").forEach(button => {
  button.addEventListener("click", () => {
    const filter = button.dataset.filter;
    document.querySelectorAll("[data-filter]").forEach(item => item.classList.toggle("active", item === button));
    document.querySelectorAll("[data-kind]").forEach(card => {
      card.hidden = filter !== "todos" && card.dataset.kind !== filter;
    });
  });
});
document.getElementById("compare-detail").addEventListener("click", event => {
  const detail = document.querySelector(".compare-detail");
  detail.hidden = !detail.hidden;
  event.currentTarget.innerHTML = detail.hidden ? 'Ver evidências e limites <svg><use href="#i-arrow"/></svg>' : 'Ocultar detalhes <svg><use href="#i-arrow"/></svg>';
});
document.getElementById("request-recapture").addEventListener("click", () => {
  showToast("Pedido de recaptura simulado: o participante recebe a vista solicitada no aplicativo de captura.");
});
document.getElementById("new-inspection").addEventListener("click", () => {
  showToast("Nesta prévia, o checklist detalhado é o da entrada do Apartamento 204.");
});
