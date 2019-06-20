# Actions and C#

The following example shows a simple C# app that does the following:

1. Listens for events from an Event Source named MyEventSource
2. Publishes an event to another action named action-2
3. Registers to get all saved states when the process launches

```
public class ActionEvent
{
    public object Data { get; set; }
    public List<string> To { get; set; }
}

public class IncomingEvent
{
    public object Data { get; set; }
}

[Route("api/[controller]/[action]")]
public class ValuesApiController : ApiController
{
    private int counter;

    // Get latest state of app when process launches
	[HttpPost]
	public IEnumerable State(Dictionary<string, object> state)
	{
		this.counter = (int)state["counter"];
        return new HttpResponseMessage();
	}

    // Listen to events from MyEventSource
	[HttpPost]
	public HttpResponseMessage MyEventSource(IncomingEvent @event)
	{
        var newEvent = new ActionEvent{
            Data = "Hi",
            To = new List<string>{"action-2"}
        };

        // Publish an event to action-2
        this.PublishEventToActions(newEvent);

        return new HttpResponseMessage();
	}

    public void PublishEventToActions(ActionEvent @event)
    {
        using (var client = new HttpClient())
        {
            var json = new JavaScriptSerializer().Serialize(@event);
            var response = await client.PostAsync(
                "http://localhost:3500/publish", 
                new StringContent(json, Encoding.UTF8, "application/json"));
        }
    }
}
```