import Source from "./Source";
import Destination from "./Destination";

function App() {
  const params = new URLSearchParams(window.location.search);
  const type = params.get("type") === "source" ? "source" : "destination";

  return (
    <main className="app">
      <h1>sonic-bridge</h1>
      {type === "source" ? <Source /> : <Destination />}
    </main>
  );
}

export default App;
