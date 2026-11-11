export function activate(coddy) {
  const page = coddy.ui.registerPage({
    id: "counter",
    title: "My first extension",
    mount(element) {
      let count = 0;
      const button = document.createElement("button");
      button.textContent = "Clicked 0 times";
      button.addEventListener("click", () => {
        button.textContent = `Clicked ${++count} times`;
      }, { signal: coddy.signal });
      element.append(button);
      return () => button.remove();
    }
  });
  return () => page.remove();
}
