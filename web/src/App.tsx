import Source from "./Source";
import Destination from "./Destination";

function App() {
  const params = new URLSearchParams(window.location.search);
  const type = params.get("type") === "source" ? "source" : "destination";

  return (
    <main className="flex min-h-dvh items-center justify-center bg-neutral-950 p-6 text-neutral-50">
      {type === "source" ? <Source /> : <Destination />}
    </main>
  );
}

export default App;
