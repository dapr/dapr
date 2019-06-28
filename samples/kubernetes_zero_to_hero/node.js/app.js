const express = require('express');
var bodyParser = require('body-parser');
const app = express();
app.use(bodyParser.json());

const port = 3000;

app.post('/state', (req, res) => {
    e = req.body;

    if (e.length > 0) {
        order = e[e.length - 1].value;
    }

    res.status(200).send();
});

let order;

app.get('/order', (_req, res) => {
    res.json(order);
});

app.post('/neworder', (req, res) => {
    const data = req.body.data;
    const orderId = data.orderId;
    console.log("Got a new order! Order ID: " + orderId);

    order = data;

    res.json({
        state: [{
            key: "order",
            value: order
        }]
    });
});

app.listen(port, () => console.log(`Node App listening on port ${port}!`));